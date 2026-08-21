// Structural probe generation: candidates that synthesize a forbidden
// structural state — an import-boundary breach, a broken interface
// satisfaction — instead of mutating a function body. A structural kill
// is evidence about the oracle's teeth, never about the soundness of
// the analyzer behind it (REQ-target-structural).
package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ImportProbe is one import-boundary candidate: a synthesized file
// blank-importing the forbidden path inside the scoped package.
type ImportProbe struct {
	Package string
	// File is the absolute path of the synthesized probe file inside
	// the package directory; it exists only in the mutant overlay.
	File   string
	Source []byte
}

// probeFileName is deliberately loud and sort-late: the file exists
// only inside a mutant's overlay, and a stray on-disk copy is
// immediately attributable.
const probeFileName = "zz_gomutant_structural_probe.go"

// ImportProbes synthesizes one probe per scoped package, each
// blank-importing the forbidden path: the oracle must fail on every
// one, or the boundary assertion is vacuous for that package.
func (t *Tree) ImportProbes(ctx context.Context, packages []string, forbidden string) ([]ImportProbe, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	probes := make([]ImportProbe, 0, len(packages))
	for _, pkgPath := range packages {
		var name, dir string
		for _, pkg := range t.pkgs {
			if pkg.PkgPath == pkgPath && len(pkg.GoFiles) > 0 {
				name, dir = pkg.Name, filepath.Dir(pkg.GoFiles[0])
				break
			}
		}
		if name == "" || strings.HasSuffix(name, "_test") {
			return nil, fmt.Errorf("import probe: scoped package %s is not a loaded source package", pkgPath)
		}
		if pkgPath == forbidden {
			return nil, fmt.Errorf("import probe: scoped package %s is the forbidden path itself", pkgPath)
		}
		source := fmt.Sprintf("// Synthesized structural probe: the forbidden state the oracle must refuse.\npackage %s\n\nimport _ %q\n", name, forbidden)
		probes = append(probes, ImportProbe{Package: pkgPath, File: filepath.Join(dir, probeFileName), Source: []byte(source)})
	}
	return probes, nil
}

// MethodProbe is one interface-satisfaction candidate: the declaring
// file rewritten with one asserted method renamed, so the type no
// longer satisfies the interface through it.
type MethodProbe struct {
	Method string
	File   string
	Source []byte
}

// MethodProbes synthesizes one candidate per method of the interface's
// method set, each renaming the type's declaration of that method in
// its declaring file: the oracle must fail on every one, or the
// satisfaction assertion has no teeth for that method.
func (t *Tree) MethodProbes(ctx context.Context, typeSymbol, ifaceSymbol string) ([]MethodProbe, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ifaceObj, err := t.objectContext(ctx, ifaceSymbol)
	if err != nil {
		return nil, fmt.Errorf("method probe: interface %s does not resolve: %w", ifaceSymbol, err)
	}
	typeObj, err := t.objectContext(ctx, typeSymbol)
	if err != nil {
		return nil, fmt.Errorf("method probe: type %s does not resolve: %w", typeSymbol, err)
	}
	names, err := interfaceMethodNames(ifaceObj)
	if err != nil {
		return nil, fmt.Errorf("method probe: %s: %w", ifaceSymbol, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("method probe: interface %s has an empty method set — nothing to break", ifaceSymbol)
	}
	typeName := typeObj.Name()
	var probes []MethodProbe
	for _, method := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, src, ok := t.methodDeclarationRewrite(typeSymbol, typeName, method)
		if !ok {
			return nil, fmt.Errorf("method probe: %s declares no method %s in a loaded source file — the type may satisfy %s through embedding, which this class does not break", typeSymbol, method, ifaceSymbol)
		}
		probes = append(probes, MethodProbe{Method: method, File: file, Source: src})
	}
	return probes, nil
}

// interfaceMethodNames enumerates an interface object's method set in
// deterministic order.
func interfaceMethodNames(obj types.Object) ([]string, error) {
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("not an interface type")
	}
	names := make([]string, 0, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		names = append(names, iface.Method(i).Name())
	}
	sort.Strings(names)
	return names, nil
}

// methodDeclarationRewrite finds typeName's declaration of method in
// the type's package and returns the declaring file rewritten with the
// method renamed — a rename no interface can see through.
func (t *Tree) methodDeclarationRewrite(typeSymbol, typeName, method string) (string, []byte, bool) {
	pkgPath, _ := t.splitSymbol(typeSymbol)
	for _, pkg := range t.pkgs {
		if pkg.PkgPath != pkgPath {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != method {
					continue
				}
				if receiverTypeName(fn) != typeName {
					continue
				}
				file := pkg.Fset.Position(fn.Name.Pos()).Filename
				src, err := os.ReadFile(file)
				if err != nil {
					return "", nil, false
				}
				offset := pkg.Fset.Position(fn.Name.Pos()).Offset
				end := pkg.Fset.Position(fn.Name.End()).Offset
				if offset < 0 || end > len(src) || string(src[offset:end]) != method {
					return "", nil, false
				}
				renamed := append([]byte(nil), src[:offset]...)
				renamed = append(renamed, []byte(method+"_gomutantStructuralProbe")...)
				renamed = append(renamed, src[end:]...)
				return file, renamed, true
			}
		}
	}
	return "", nil, false
}

// receiverTypeName unwraps a method receiver to its named type.
func receiverTypeName(fn *ast.FuncDecl) string {
	if len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}
