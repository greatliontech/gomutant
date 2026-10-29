package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
)

// funcSchemaPiece holds one symbol's validated switch body and its byte
// coordinates in the original source file so MergeCandidateSchemas can
// splice multiple functions of the same package into one package-wide
// schema overlay.
type funcSchemaPiece struct {
	pkgPath           string
	filePath          string
	origSource        []byte
	headerOffset      int
	bodyStart         int
	bodyEnd           int
	switchBody        []byte
	varName           string
	firstFileInitRepl *Replacement
}

type schemaCaseArm struct {
	candidateIdx int
	mutantID     string
	innerBody    []byte
}

var loadOSTypesPackage = sync.OnceValues(func() (*types.Package, error) {
	return importer.Default().Import("os")
})

func schemaActiveVarName(filePath string) string {
	sum := sha256.Sum256([]byte(filePath))
	return "_gomutantActive_" + hex.EncodeToString(sum[:4])
}

func hasLineDirective(src []byte) bool {
	return bytes.Contains(src, []byte("//line ")) || bytes.Contains(src, []byte("/*line "))
}

func headerInsertOffset(tokens *token.File, file *ast.File, src []byte) int {
	off := tokens.Offset(file.Name.End())
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			break
		}
		off = tokens.Offset(gen.End())
	}
	for off < len(src) && (src[off] == ' ' || src[off] == '\t' || src[off] == ';') {
		off++
	}
	return off
}

func schemaHeaderBytes(varName string) []byte {
	valName := varName + "_val"
	return []byte(fmt.Sprintf(
		"\nimport _gomutant_os \"os\"\nvar %s string\nfunc %s() string {\n\tif %s == \"\" {\n\t\tif v := _gomutant_os.Getenv(\"GOMUTANT_ACTIVE_MUTANT\"); v != \"\" {\n\t\t\t%s = v\n\t\t} else {\n\t\t\t%s = \"\\x00\"\n\t\t\tif len(_gomutant_os.Args) > 0 {\n\t\t\t\ta := _gomutant_os.Args[0]\n\t\t\t\tconst tag = \"_gomutant_active_\"\n\t\t\t\tfor i := len(a) - len(tag); i >= 0; i-- {\n\t\t\t\t\tif a[i:i+len(tag)] == tag {\n\t\t\t\t\t\ts := a[i+len(tag):]\n\t\t\t\t\t\tfor j := 0; j < len(s); j++ {\n\t\t\t\t\t\t\tif s[j] == '/' || s[j] == '\\\\' {\n\t\t\t\t\t\t\t\ts = s[:j]\n\t\t\t\t\t\t\t\tbreak\n\t\t\t\t\t\t\t}\n\t\t\t\t\t\t}\n\t\t\t\t\t\tif len(s) > 4 && s[len(s)-4:] == \".exe\" {\n\t\t\t\t\t\t\ts = s[:len(s)-4]\n\t\t\t\t\t\t}\n\t\t\t\t\t\tif s != \"\" {\n\t\t\t\t\t\t\t%s = s\n\t\t\t\t\t\t}\n\t\t\t\t\t\tbreak\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n\treturn %s\n}\nvar _ = %s()\nfunc init() { _ = _gomutant_os.Unsetenv(\"GOMUTANT_ACTIVE_MUTANT\") }\n",
		valName, varName, valName, valName, valName, valName, valName, varName,
	))
}

func buildFirstFileInitReplacement(catalog *catalog) (*Replacement, bool) {
	if catalog == nil || catalog.pkg == nil || catalog.pkg.Fset == nil || len(catalog.pkg.Syntax) == 0 {
		return nil, false
	}
	firstFile := catalog.pkg.Syntax[0]
	if firstFile == nil || firstFile == catalog.file {
		return nil, true
	}
	firstPath := catalog.pkg.Fset.PositionFor(firstFile.Pos(), false).Filename
	firstTokens := catalog.pkg.Fset.File(firstFile.Pos())
	if firstPath == "" || firstTokens == nil {
		return nil, false
	}
	firstSrc, err := os.ReadFile(firstPath)
	if err != nil {
		return nil, false
	}
	if hasLineDirective(firstSrc) || bytes.Contains(firstSrc, []byte("_gomutant_os")) {
		return nil, false
	}
	firstHeaderOff := headerInsertOffset(firstTokens, firstFile, firstSrc)
	if firstHeaderOff < 0 || firstHeaderOff > len(firstSrc) {
		return nil, false
	}
	initHeader := []byte("\nimport _gomutant_os \"os\"\nfunc init() { _ = _gomutant_os.Unsetenv(\"GOMUTANT_ACTIVE_MUTANT\") }\n")
	var raw bytes.Buffer
	raw.Grow(len(firstSrc) + len(initHeader))
	raw.Write(firstSrc[:firstHeaderOff])
	raw.Write(initHeader)
	raw.Write(firstSrc[firstHeaderOff:])
	formatted, err := format.Source(raw.Bytes())
	if err != nil {
		return nil, false
	}
	return &Replacement{File: firstPath, Source: formatted}, true
}

func bodyRedeclaresSignatureVar(fd *ast.FuncDecl) bool {
	if fd == nil || fd.Body == nil {
		return false
	}
	sigVars := map[string]bool{}
	addFieldList := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, field := range fl.List {
			if field == nil {
				continue
			}
			for _, name := range field.Names {
				if name != nil && name.Name != "" && name.Name != "_" {
					sigVars[name.Name] = true
				}
			}
		}
	}
	addFieldList(fd.Recv)
	if fd.Type != nil {
		addFieldList(fd.Type.Params)
		addFieldList(fd.Type.Results)
	}
	if len(sigVars) == 0 {
		return false
	}
	for _, stmt := range fd.Body.List {
		for {
			lbl, ok := stmt.(*ast.LabeledStmt)
			if !ok || lbl == nil {
				break
			}
			stmt = lbl.Stmt
		}
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign == nil || assign.Tok != token.DEFINE {
			continue
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id != nil && sigVars[id.Name] {
				return true
			}
		}
	}
	return false
}

func funcDeclOrdinal(file *ast.File, target *ast.FuncDecl) int {
	ord := 0
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd == target {
			return ord
		}
		ord++
	}
	return -1
}

func nthFuncDecl(file *ast.File, ord int) *ast.FuncDecl {
	cur := 0
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if cur == ord {
			return fd
		}
		cur++
	}
	return nil
}

func importSpecSet(file *ast.File) map[string]bool {
	out := make(map[string]bool, len(file.Imports))
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name+"\x00"+imp.Path.Value] = true
	}
	return out
}

func importsSubset(sub, super map[string]bool) bool {
	for k := range sub {
		if !super[k] {
			return false
		}
	}
	return true
}

func renameBodyLabels(body *ast.BlockStmt, suffix string) {
	if body == nil {
		return
	}
	var declared map[string]bool
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
			return false
		}
		if lbl, ok := n.(*ast.LabeledStmt); ok && lbl.Label != nil {
			if declared == nil {
				declared = map[string]bool{}
			}
			declared[lbl.Label.Name] = true
		}
		return true
	})
	if len(declared) == 0 {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
			return false
		}
		switch stmt := n.(type) {
		case *ast.LabeledStmt:
			if stmt.Label != nil && declared[stmt.Label.Name] {
				stmt.Label.Name += suffix
			}
		case *ast.BranchStmt:
			if stmt.Label != nil && declared[stmt.Label.Name] {
				stmt.Label.Name += suffix
			}
		}
		return true
	})
}

func innerBlockBytes(formattedBlock []byte) ([]byte, bool) {
	open := bytes.IndexByte(formattedBlock, '{')
	close := bytes.LastIndexByte(formattedBlock, '}')
	if open < 0 || close <= open {
		return nil, false
	}
	inner := formattedBlock[open+1 : close]
	inner = bytes.TrimPrefix(inner, []byte("\n"))
	return inner, true
}

func extractFuncBodyInner(filePath string, src []byte, declOrdinal int, expectedName, labelSuffix string) ([]byte, map[string]bool, bool) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, nil, false
	}
	fd := nthFuncDecl(parsed, declOrdinal)
	if fd == nil || fd.Body == nil || fd.Name == nil || fd.Name.Name != expectedName {
		return nil, nil, false
	}
	if bodyRedeclaresSignatureVar(fd) {
		return nil, nil, false
	}
	renameBodyLabels(fd.Body, labelSuffix)
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, fd.Body); err != nil {
		return nil, nil, false
	}
	inner, ok := innerBlockBytes(buf.Bytes())
	if !ok {
		return nil, nil, false
	}
	return inner, importSpecSet(parsed), true
}

func buildSwitchBody(varName string, arms []schemaCaseArm, defaultInner []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n\tswitch %s() {\n", varName)
	for _, arm := range arms {
		fmt.Fprintf(&buf, "\tcase %q:\n", arm.mutantID)
		buf.Write(arm.innerBody)
		if len(arm.innerBody) == 0 || arm.innerBody[len(arm.innerBody)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	buf.WriteString("\tdefault:\n")
	buf.Write(defaultInner)
	if len(defaultInner) == 0 || defaultInner[len(defaultInner)-1] != '\n' {
		buf.WriteByte('\n')
	}
	buf.WriteString("\t}\n")
	return buf.Bytes()
}

func spliceSchemaFile(origSource []byte, headerOffset int, varName string, pieces []*funcSchemaPiece) ([]byte, error) {
	if headerOffset < 0 || headerOffset > len(origSource) || len(pieces) == 0 {
		return nil, fmt.Errorf("invalid schema splice bounds")
	}
	sorted := slices.Clone(pieces)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].bodyStart < sorted[j].bodyStart
	})
	for i, p := range sorted {
		if p.bodyStart < headerOffset || p.bodyEnd < p.bodyStart || p.bodyEnd > len(origSource) {
			return nil, fmt.Errorf("invalid piece bounds [%d,%d)", p.bodyStart, p.bodyEnd)
		}
		if i > 0 && p.bodyStart < sorted[i-1].bodyEnd {
			return nil, fmt.Errorf("overlapping piece bounds")
		}
	}
	header := schemaHeaderBytes(varName)
	var raw bytes.Buffer
	raw.Grow(len(origSource) + len(header) + 1024*len(sorted))
	raw.Write(origSource[:headerOffset])
	raw.Write(header)
	cursor := headerOffset
	for _, p := range sorted {
		raw.Write(origSource[cursor:p.bodyStart])
		raw.Write(p.switchBody)
		cursor = p.bodyEnd
	}
	raw.Write(origSource[cursor:])
	return format.Source(raw.Bytes())
}

type schemaImporter struct {
	byPath map[string]*types.Package
}

func (s schemaImporter) Import(path string) (*types.Package, error) {
	if path == "os" {
		return loadOSTypesPackage()
	}
	if pkg, ok := s.byPath[path]; ok && pkg != nil {
		return pkg, nil
	}
	return importer.Default().Import(path)
}

func buildSchemaImporter(catalog *catalog) schemaImporter {
	byPath := map[string]*types.Package{}
	if catalog.pkg != nil && catalog.pkg.Types != nil {
		for _, imp := range catalog.pkg.Types.Imports() {
			if imp != nil {
				byPath[imp.Path()] = imp
			}
		}
	}
	if catalog.pkg != nil && catalog.pkg.TypesInfo != nil {
		for _, f := range catalog.pkg.Syntax {
			for _, spec := range f.Imports {
				if spec == nil || spec.Path == nil {
					continue
				}
				rawPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				if obj, ok := catalog.pkg.TypesInfo.Implicits[spec]; ok {
					if pkgName, ok := obj.(*types.PkgName); ok && pkgName.Imported() != nil {
						byPath[rawPath] = pkgName.Imported()
					}
				} else if spec.Name != nil {
					if obj, ok := catalog.pkg.TypesInfo.Defs[spec.Name]; ok {
						if pkgName, ok := obj.(*types.PkgName); ok && pkgName.Imported() != nil {
							byPath[rawPath] = pkgName.Imported()
						}
					}
				}
			}
		}
	}
	return schemaImporter{byPath: byPath}
}

var schemaTypecheckMu sync.Mutex

func schemaTypecheckFile(catalog *catalog, src []byte) error {
	if catalog == nil || catalog.pkg == nil || catalog.pkg.Types == nil || catalog.pkg.Fset == nil || catalog.file == nil {
		return fmt.Errorf("missing package types")
	}
	schemaTypecheckMu.Lock()
	defer schemaTypecheckMu.Unlock()
	fset := catalog.pkg.Fset
	parsed, err := parser.ParseFile(fset, catalog.path, src, 0)
	if err != nil {
		return err
	}
	parsed.GoVersion = catalog.file.GoVersion
	files := make([]*ast.File, 0, len(catalog.pkg.Syntax))
	replaced := false
	for _, f := range catalog.pkg.Syntax {
		if f == catalog.file {
			files = append(files, parsed)
			replaced = true
			continue
		}
		files = append(files, f)
	}
	if !replaced {
		return fmt.Errorf("target file not found in package syntax")
	}
	conf := types.Config{
		Importer: buildSchemaImporter(catalog),
		Sizes:    catalog.pkg.TypesSizes,
		Error:    func(error) {},
	}
	_, err = conf.Check(catalog.pkg.Types.Path(), fset, files, nil)
	return err
}

func specEditsInsideBody(edits []sourceEdit, bodyOpen, bodyClose int) bool {
	if len(edits) == 0 {
		return false
	}
	for _, e := range edits {
		if e.start <= bodyOpen || e.end > bodyClose || e.end < e.start {
			return false
		}
	}
	return true
}

func schemaReplacementsKey(repls []Replacement) string {
	h := sha256.New()
	for _, r := range repls {
		h.Write([]byte(r.File))
		h.Write([]byte{0})
		h.Write(r.Source)
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func candidateMayStrandImport(catalog *catalog, edits []sourceEdit, alwaysProcessImports bool, pkgNameOffsets []int) bool {
	if alwaysProcessImports || specTouchesPkgName(pkgNameOffsets, edits) {
		return true
	}
	if catalog == nil || catalog.file == nil {
		return false
	}
	var importNames []string
	for _, imp := range catalog.file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "." {
				return true
			}
			if imp.Name.Name != "_" && imp.Name.Name != "" {
				importNames = append(importNames, imp.Name.Name)
			}
			continue
		}
		rawPath, err := strconv.Unquote(imp.Path.Value)
		if err == nil && rawPath != "" {
			base := filepath.Base(rawPath)
			if base != "" && base != "." {
				importNames = append(importNames, base)
			}
		}
	}
	if len(importNames) == 0 {
		return false
	}
	for _, e := range edits {
		if e.start < 0 || e.end > len(catalog.source) || e.end <= e.start {
			continue
		}
		deleted := catalog.source[e.start:e.end]
		for _, name := range importNames {
			if bytes.Contains(deleted, []byte(name)) {
				return true
			}
		}
	}
	return false
}

func attachSymbolSchema(ctx context.Context, catalog *catalog, symbol string, specs []candidateSpec, candidates []Candidate) {
	if catalog == nil || catalog.fd == nil || catalog.fd.Body == nil || catalog.fd.Name == nil ||
		catalog.file == nil || catalog.tokens == nil || catalog.pkg == nil || catalog.pkg.Types == nil {
		return
	}
	if len(candidates) == 0 || len(specs) != len(candidates) {
		return
	}
	if hasLineDirective(catalog.source) || bodyRedeclaresSignatureVar(catalog.fd) {
		return
	}
	varName := schemaActiveVarName(catalog.path)
	if bytes.Contains(catalog.source, []byte("_gomutant_os")) || bytes.Contains(catalog.source, []byte(varName)) {
		return
	}
	firstFileInitRepl, ok := buildFirstFileInitReplacement(catalog)
	if !ok {
		return
	}
	bodyOpen := catalog.tokens.Offset(catalog.fd.Body.Lbrace)
	bodyClose := catalog.tokens.Offset(catalog.fd.Body.Rbrace)
	if bodyOpen < 0 || bodyClose <= bodyOpen || bodyClose > len(catalog.source) {
		return
	}
	headerOff := headerInsertOffset(catalog.tokens, catalog.file, catalog.source)
	if headerOff < 0 || headerOff > bodyOpen {
		return
	}
	declOrd := funcDeclOrdinal(catalog.file, catalog.fd)
	if declOrd < 0 {
		return
	}
	funcName := catalog.fd.Name.Name
	defaultInner, origImports, ok := extractFuncBodyInner(catalog.path, catalog.source, declOrd, funcName, "_gomutant_default")
	if !ok {
		return
	}
	alwaysProcessImports, pkgNameOffsets := declImportReferences(catalog)
	symHash := sha256.Sum256([]byte(symbol))
	var arms []schemaCaseArm
	for i := range candidates {
		if ctx.Err() != nil {
			return
		}
		if len(candidates[i].Replacements) != 1 || candidates[i].Replacements[0].File != catalog.path || candidates[i].Replacements[0].Synthetic {
			continue
		}
		if !specEditsInsideBody(specs[i].edits, bodyOpen, bodyClose) {
			continue
		}
		armInner, mutImports, ok := extractFuncBodyInner(
			catalog.path,
			candidates[i].Replacements[0].Source,
			declOrd,
			funcName,
			fmt.Sprintf("_gomutant_%d", i+1),
		)
		if !ok || !importsSubset(mutImports, origImports) {
			continue
		}
		if len(mutImports) != len(origImports) || candidateMayStrandImport(catalog, specs[i].edits, alwaysProcessImports, pkgNameOffsets) {
			if schemaTypecheckFile(catalog, candidates[i].Replacements[0].Source) != nil {
				continue
			}
		}
		arms = append(arms, schemaCaseArm{
			candidateIdx: i,
			mutantID:     fmt.Sprintf("%x_%d", symHash[:6], i+1),
			innerBody:    armInner,
		})
	}
	if len(arms) == 0 {
		return
	}
	pkgPath := catalog.pkg.PkgPath
	if pkgPath == "" && catalog.pkg.Types != nil {
		pkgPath = catalog.pkg.Types.Path()
	}
	buildPieceAndSource := func(activeArms []schemaCaseArm) (*funcSchemaPiece, []byte, error) {
		sw := buildSwitchBody(varName, activeArms, defaultInner)
		piece := &funcSchemaPiece{
			pkgPath:           pkgPath,
			filePath:          catalog.path,
			origSource:        catalog.source,
			headerOffset:      headerOff,
			bodyStart:         bodyOpen + 1,
			bodyEnd:           bodyClose,
			switchBody:        sw,
			varName:           varName,
			firstFileInitRepl: firstFileInitRepl,
		}
		src, err := spliceSchemaFile(catalog.source, headerOff, varName, []*funcSchemaPiece{piece})
		if err != nil {
			return nil, nil, err
		}
		return piece, src, nil
	}
	piece, schemaSrc, err := buildPieceAndSource(arms)
	if err != nil {
		return
	}
	if err := schemaTypecheckFile(catalog, schemaSrc); err != nil {
		if schemaTypecheckFile(catalog, catalog.source) != nil {
			return
		}
		filtered := make([]schemaCaseArm, 0, len(arms))
		for _, arm := range arms {
			if ctx.Err() != nil {
				return
			}
			if schemaTypecheckFile(catalog, candidates[arm.candidateIdx].Replacements[0].Source) == nil {
				filtered = append(filtered, arm)
			}
		}
		if len(filtered) == 0 {
			return
		}
		arms = filtered
		piece, schemaSrc, err = buildPieceAndSource(arms)
		if err != nil || schemaTypecheckFile(catalog, schemaSrc) != nil {
			return
		}
	}
	repls := []Replacement{{File: catalog.path, Source: schemaSrc}}
	if firstFileInitRepl != nil {
		repls = append(repls, *firstFileInitRepl)
		sort.Slice(repls, func(i, j int) bool {
			return repls[i].File < repls[j].File
		})
	}
	key := schemaReplacementsKey(repls)
	for _, arm := range arms {
		idx := arm.candidateIdx
		candidates[idx].SchemaReplacements = repls
		candidates[idx].SchemaMutantID = arm.mutantID
		candidates[idx].SchemaKey = key
		candidates[idx].schemaPiece = piece
	}
}

// MergeCandidateSchemas merges per-symbol mutant schemas across all candidate
// slices belonging to the same Go package into a single package-wide schema
// replacement set and key, so one `go test -c -overlay` binary serves every
// mutated function in that package.
func MergeCandidateSchemas(groups ...[]Candidate) {
	byPkg := map[string][]*funcSchemaPiece{}
	seenPiece := map[*funcSchemaPiece]bool{}
	for _, group := range groups {
		for i := range group {
			p := group[i].schemaPiece
			if p == nil || p.pkgPath == "" || group[i].SchemaMutantID == "" || seenPiece[p] {
				continue
			}
			seenPiece[p] = true
			byPkg[p.pkgPath] = append(byPkg[p.pkgPath], p)
		}
	}
	for pkgPath, pieces := range byPkg {
		if len(pieces) <= 1 {
			continue
		}
		byFile := map[string][]*funcSchemaPiece{}
		var files []string
		var firstInit *Replacement
		for _, p := range pieces {
			if p.firstFileInitRepl != nil && firstInit == nil {
				firstInit = p.firstFileInitRepl
			}
			if _, ok := byFile[p.filePath]; !ok {
				files = append(files, p.filePath)
			}
			byFile[p.filePath] = append(byFile[p.filePath], p)
		}
		sort.Strings(files)
		mergedRepls := make([]Replacement, 0, len(files)+1)
		validPkg := true
		for _, filePath := range files {
			filePieces := byFile[filePath]
			first := filePieces[0]
			mergedSrc, err := spliceSchemaFile(first.origSource, first.headerOffset, first.varName, filePieces)
			if err != nil {
				validPkg = false
				break
			}
			mergedRepls = append(mergedRepls, Replacement{File: filePath, Source: mergedSrc})
		}
		if !validPkg || len(mergedRepls) == 0 {
			continue
		}
		if firstInit != nil {
			if _, alreadyMutated := byFile[firstInit.File]; !alreadyMutated {
				mergedRepls = append(mergedRepls, *firstInit)
				sort.Slice(mergedRepls, func(i, j int) bool {
					return mergedRepls[i].File < mergedRepls[j].File
				})
			}
		}
		mergedKey := schemaReplacementsKey(mergedRepls)
		for _, group := range groups {
			for i := range group {
				p := group[i].schemaPiece
				if p != nil && p.pkgPath == pkgPath && group[i].SchemaMutantID != "" {
					group[i].SchemaReplacements = mergedRepls
					group[i].SchemaKey = mergedKey
				}
			}
		}
	}
}
