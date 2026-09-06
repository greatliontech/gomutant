package gomutant

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
)

// pruneUnusedImports drops the imports a mutant source no longer uses:
// a probe that deletes a guard whole strands the guard's error path's
// imports ("fmt imported and not used"), and a probe declares no import
// intent, so pruning an import nothing references cannot change the
// mutant's meaning — where padding the source to keep the import would
// measure a different mutant. Only imports whose bound name is known
// are judged: an explicit alias, or the declared package name the
// loaded package imports it under; a new import of a package the file's
// package never imported keeps its unknown name and is left alone
// (guessing the name from the path's last element would prune a used
// import under a versioned or hyphenated path and turn a clean mutant
// into a compile failure). Blank and dot imports are never pruned. The
// pruned import paths are returned, so the result states what
// happened; a source that does not parse is returned untouched, its
// compile failure the caller's to report (REQ-exec-ephemeral).
func pruneUnusedImports(source []byte, importedName func(importPath string) (string, bool)) ([]byte, []string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", source, parser.ParseComments)
	if err != nil {
		return source, nil
	}
	type unused struct{ alias, path string }
	var drop []unused
	var pruned []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name, alias := "", ""
		if spec.Name != nil {
			name, alias = spec.Name.Name, spec.Name.Name
		} else if declared, ok := importedName(path); ok {
			name = declared
		}
		if name == "" || name == "_" || name == "." {
			continue
		}
		if usesPackageName(file, name) {
			continue
		}
		drop = append(drop, unused{alias: alias, path: path})
		pruned = append(pruned, path)
	}
	if len(pruned) == 0 {
		return source, nil
	}
	// The deletions mutate file.Imports; they run after the walk.
	for _, u := range drop {
		astutil.DeleteNamedImport(fset, file, u.alias, u.path)
	}
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return source, nil
	}
	return out.Bytes(), pruned
}

// usesPackageName reports whether the file references name as the
// qualifier of a selector — the one way an imported package name is
// used.
func usesPackageName(file *ast.File, name string) bool {
	used := false
	ast.Inspect(file, func(n ast.Node) bool {
		if used {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == name {
				used = true
				return false
			}
		}
		return true
	})
	return used
}
