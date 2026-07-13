package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"github.com/greatliontech/gomutant/internal/contextio"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/tools/go/packages"
)

// ErrNotFunction marks a resolvable symbol with no function body — a type or
// a variable. Body-level operations skip such symbols; there is nothing to
// hash or mutate.
var ErrNotFunction = errors.New("is not a function or method")

// BodyHash hashes the canonical text of the symbol's body source — the
// function or method body when there is one, the whole declaration otherwise
// (REQ-result-record). It moves when behavior-bearing code moves and ignores
// formatting churn.
func (t *Tree) BodyHash(symbol string) (string, error) {
	return t.BodyHashContext(context.Background(), symbol)
}

// BodyHashContext is BodyHash with cooperative cancellation.
func (t *Tree) BodyHashContext(ctx context.Context, symbol string) (string, error) {
	fd, pkg, err := t.funcDeclContext(ctx, symbol)
	if err != nil {
		return "", err
	}
	node := ast.Node(fd)
	if fd.Body != nil {
		node = fd.Body
	}
	src, err := t.sourceOfContext(ctx, pkg, node)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return canonHash(string(src)), ctx.Err()
}

// canonText is the canonical form hashes are computed over: Unicode NFC,
// every run of Unicode whitespace collapsed to a single space, leading and
// trailing whitespace removed. It is a normalization projection — applying
// it twice yields the same bytes as applying it once — so the hash never
// depends on formatting.
func canonText(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}

// canonHash is the SHA-256 digest of the UTF-8 bytes of canonText(s), as 64
// lowercase hexadecimal characters.
func canonHash(s string) string {
	sum := sha256.Sum256([]byte(canonText(s)))
	return hex.EncodeToString(sum[:])
}

// funcDecl resolves a symbol to its declaring FuncDecl and package.
func (t *Tree) funcDecl(symbol string) (*ast.FuncDecl, *packages.Package, error) {
	return t.funcDeclContext(context.Background(), symbol)
}

func (t *Tree) funcDeclContext(ctx context.Context, symbol string) (*ast.FuncDecl, *packages.Package, error) {
	obj, err := t.objectContext(ctx, symbol)
	if err != nil {
		return nil, nil, err
	}
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for _, f := range pkg.Syntax {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if pkg.TypesInfo.Defs[fd.Name] == obj {
					return fd, pkg, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("symbol %s: %w", symbol, ErrNotFunction)
}

// object resolves a symbol to its types.Object, or an error naming what
// failed: an unmatched import path, a load-broken package, or a missing
// identifier.
func (t *Tree) object(symbol string) (types.Object, error) {
	return t.objectContext(context.Background(), symbol)
}

func (t *Tree) objectContext(ctx context.Context, symbol string) (types.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pkgPath, rest := t.splitSymbol(symbol)
	if pkgPath == "" {
		return nil, fmt.Errorf("symbol %s: no loaded package matches its import path", symbol)
	}
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return nil, fmt.Errorf("symbol %s: want <pkg>.<Ident> or <pkg>.<Receiver>.<Method>", symbol)
	}
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pkg.PkgPath != pkgPath && pkg.PkgPath != pkgPath+"_test" {
			continue
		}
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("package %s has load errors: %v", pkg.ID, pkg.Errors[0])
		}
		if obj := lookup(pkg.Types, parts); obj != nil {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("symbol %s does not resolve", symbol)
}

// splitSymbol finds the loaded package whose path prefixes the symbol
// (longest match wins) and returns it with the remainder.
func (t *Tree) splitSymbol(symbol string) (string, string) {
	best := ""
	for _, pkg := range t.pkgs {
		p := basePackagePath(pkg)
		if strings.HasPrefix(symbol, p+".") && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return "", ""
	}
	return best, strings.TrimPrefix(symbol, best+".")
}

// PackagePath returns the loaded Go import path that owns symbol.
func (t *Tree) PackagePath(symbol string) (string, error) {
	return t.PackagePathContext(context.Background(), symbol)
}

// PackagePathContext is PackagePath with caller-owned cancellation.
func (t *Tree) PackagePathContext(ctx context.Context, symbol string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pkgPath, _ := t.splitSymbol(symbol)
	if pkgPath == "" {
		return "", fmt.Errorf("symbol %s: no loaded package matches its import path", symbol)
	}
	return pkgPath, ctx.Err()
}

// lookup finds a package-scope object, or a method through its receiver type
// name.
func lookup(pkg *types.Package, parts []string) types.Object {
	obj := pkg.Scope().Lookup(parts[0])
	if obj == nil {
		return nil
	}
	if len(parts) == 1 {
		return obj
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil
	}
	// The pointer method set includes both pointer- and value-receiver
	// methods — but is empty for interface types, so fall back to the value
	// method set.
	for _, ms := range []*types.MethodSet{
		types.NewMethodSet(types.NewPointer(tn.Type())),
		types.NewMethodSet(tn.Type()),
	} {
		for i := 0; i < ms.Len(); i++ {
			if m := ms.At(i).Obj(); m.Name() == parts[1] {
				return m
			}
		}
	}
	return nil
}

// sourceOf reads the original source bytes spanned by node.
func (t *Tree) sourceOf(pkg *packages.Package, node ast.Node) ([]byte, error) {
	return t.sourceOfContext(context.Background(), pkg, node)
}

func (t *Tree) sourceOfContext(ctx context.Context, pkg *packages.Package, node ast.Node) ([]byte, error) {
	start := pkg.Fset.Position(node.Pos())
	end := pkg.Fset.Position(node.End())
	data, err := contextio.ReadFile(ctx, start.Filename)
	if err != nil {
		return nil, err
	}
	if start.Offset < 0 || end.Offset > len(data) || start.Offset > end.Offset {
		return nil, fmt.Errorf("node span out of range in %s", start.Filename)
	}
	return data[start.Offset:end.Offset], nil
}
