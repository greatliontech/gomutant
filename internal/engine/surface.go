package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// FileSurface is the Go content of one changed source file, tree-relative:
// whether it is Go at all, whether it is generated, whether it is a test
// file, and the package-level function and method symbols whose bodies
// changed. It is the per-file input to changed-scope targeting
// (REQ-target-changed): symbols become targets, and a file yielding none is
// the reported residue.
type FileSurface struct {
	Path      string
	IsGo      bool
	IsTest    bool
	Generated bool
	// Loaded reports whether the loaded packages cover the path: false for a
	// deleted, unparseable, or build-constraint-excluded file, whose bodies
	// the engine cannot see — an unbound surface, never silently classified.
	Loaded bool
	// DeclaredBodies counts the path's top-level function declarations, so a
	// changed file declaring none is distinguishable from one whose bodies
	// all match the reference.
	DeclaredBodies int
	// RefOnlyDecls counts declarations present at the reference but absent
	// from the working file — deletions, which yield no target (nothing
	// remains to mutate) but are part of the changed surface.
	RefOnlyDecls int
	// Symbols are the resolver symbol strings of the declarations whose body
	// differs from the reference version, sorted.
	Symbols []string
}

// Surface classifies the given tree-relative, slash-separated paths by Go
// content, reporting only the symbols whose body actually changed: head
// supplies a path's reference bytes (ok=false when the path is new, so every
// symbol reads as changed). A symbol's body hash is compared against the
// same hash of the reference declaration of the same name — an unchanged
// body is dropped, so a one-function edit in a thirty-function file surfaces
// one symbol, not thirty (REQ-target-changed). A path the loaded packages do
// not cover is still reported: IsGo is decided by extension, so a
// new-but-unloadable .go file reads as Go with no declared symbols rather
// than vanishing. Test files carry IsTest and no symbols — test sources are
// oracles, never targets.
func (t *Tree) Surface(paths []string, ref func(path string) ([]byte, bool)) []FileSurface {
	// Working-side declarations per tree-relative path: each declaration's
	// full resolver symbol and its body hash, keyed by short name so the
	// reference comparison matches within the file.
	type decl struct {
		symbol string
		hash   string
	}
	type fileDecls struct {
		generated bool
		byKey     map[string]decl
	}
	byPath := map[string]*fileDecls{}
	for _, pkg := range t.pkgs {
		pkgPath := strings.TrimSuffix(pkg.PkgPath, "_test")
		for _, f := range pkg.Syntax {
			abs := pkg.Fset.Position(f.Pos()).Filename
			rel, err := filepath.Rel(t.dir, abs)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			// Load(Tests: true) yields each file in both its normal and its
			// test-variant package; the AST is identical, so populate once
			// per path on first sighting to avoid double-counting.
			if _, seen := byPath[rel]; seen {
				continue
			}
			fdecls := &fileDecls{generated: ast.IsGenerated(f), byKey: map[string]decl{}}
			byPath[rel] = fdecls
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				sym := declSymbol(pkgPath, fn)
				if sym == "" {
					continue
				}
				src, err := t.sourceOf(pkg, bodyNode(fn))
				if err != nil {
					continue
				}
				fdecls.byKey[declKey(fn)] = decl{symbol: sym, hash: canonHash(string(src))}
			}
		}
	}
	out := make([]FileSurface, 0, len(paths))
	for _, p := range paths {
		fs := FileSurface{Path: p, IsGo: strings.HasSuffix(p, ".go"), IsTest: strings.HasSuffix(p, "_test.go")}
		// Test files are oracles, never mutation targets: classified, no
		// symbols surfaced (REQ-target-changed).
		if fs.IsTest {
			out = append(out, fs)
			continue
		}
		if d := byPath[p]; d != nil {
			fs.Loaded = true
			fs.Generated = d.generated
			fs.DeclaredBodies = len(d.byKey)
			var old map[string]string
			if rb, ok := ref(p); ok {
				old = refDeclHashes(rb)
			}
			for key, wd := range d.byKey {
				if old != nil && old[key] == wd.hash {
					continue // body unchanged since the reference
				}
				fs.Symbols = append(fs.Symbols, wd.symbol)
			}
			for key := range old {
				if _, ok := d.byKey[key]; !ok {
					fs.RefOnlyDecls++
				}
			}
			sort.Strings(fs.Symbols)
		}
		out = append(out, fs)
	}
	return out
}

// refDeclHashes parses reference file bytes and returns each top-level
// function or method's body hash, keyed by short name — the reference the
// working surface is diffed against. An unparseable prior version yields
// nil, so every working symbol reads as changed (conservative,
// REQ-target-changed).
func refDeclHashes(src []byte) map[string]string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		node := bodyNode(fn)
		start := fset.Position(node.Pos()).Offset
		end := fset.Position(node.End()).Offset
		if start < 0 || end > len(src) || start > end {
			continue
		}
		out[declKey(fn)] = canonHash(string(src[start:end]))
	}
	return out
}

// bodyNode is the declaration's body when it has one, else the whole
// declaration — mirroring BodyHash, so the hashed span is behavior-bearing.
func bodyNode(fd *ast.FuncDecl) ast.Node {
	if fd.Body != nil {
		return fd.Body
	}
	return fd
}

// declKey is a declaration's within-file identity for reference comparison:
// "<Name>" for a function, "<Receiver>.<Name>" for a method.
func declKey(fd *ast.FuncDecl) string {
	if recv := recvTypeName(fd); recv != "" {
		return recv + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// declSymbol builds the resolver symbol string for a top-level declaration:
// "<pkg>.<Name>" for a function, "<pkg>.<Receiver>.<Name>" for a method.
func declSymbol(pkgPath string, fd *ast.FuncDecl) string {
	if recv := recvTypeName(fd); recv != "" {
		return pkgPath + "." + recv + "." + fd.Name.Name
	}
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return "" // a receiver we cannot name — skip
	}
	return pkgPath + "." + fd.Name.Name
}

// recvTypeName is a method's receiver type name with the leading pointer
// star and any generic parameters stripped — "" for a plain function or an
// unnameable receiver.
func recvTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok { // Recv[T]
		t = idx.X
	}
	if idx, ok := t.(*ast.IndexListExpr); ok { // Recv[T, U]
		t = idx.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
