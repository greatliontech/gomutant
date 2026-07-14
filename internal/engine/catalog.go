package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"

	"golang.org/x/tools/go/packages"
)

type catalog struct {
	fd     *ast.FuncDecl
	pkg    *packages.Package
	file   *ast.File
	path   string
	source []byte
	tokens *token.File
}

type candidateEmitter func(*catalog, ast.Node, []ast.Node) []candidateSpec

func (t *Tree) candidateCatalog(ctx context.Context, symbol string) (*catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, pkg, err := t.funcDeclContext(ctx, symbol)
	if err != nil {
		return nil, err
	}
	if fd.Body == nil {
		return &catalog{fd: fd, pkg: pkg}, nil
	}
	file, path, err := t.fileOf(pkg, fd.Pos())
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &catalog{fd: fd, pkg: pkg, file: file, path: path, source: source, tokens: pkg.Fset.File(file.Pos())}, nil
}

func (c *catalog) edit(start, end token.Pos, replacement []byte) sourceEdit {
	return sourceEdit{start: c.tokens.Offset(start), end: c.tokens.Offset(end), replacement: replacement}
}

func (c *catalog) text(start, end token.Pos) []byte {
	return append([]byte(nil), c.source[c.tokens.Offset(start):c.tokens.Offset(end)]...)
}

func (c *catalog) deletionEdit(start, end token.Pos) sourceEdit {
	edit := c.edit(start, end, nil)
	cursor := edit.end
	for cursor < len(c.source) && (c.source[cursor] == ' ' || c.source[cursor] == '\t') {
		cursor++
	}
	if cursor < len(c.source) && c.source[cursor] == ';' {
		edit.end = cursor + 1
	}
	return edit
}

func (c *catalog) enumerate(ctx context.Context) ([]candidateSpec, error) {
	if c.fd.Body == nil {
		return nil, nil
	}
	var specs []candidateSpec
	var ancestors []ast.Node
	ast.Inspect(c.fd.Body, func(node ast.Node) bool {
		if ctx.Err() != nil {
			return false
		}
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		for _, emit := range activeCandidateEmitters {
			specs = append(specs, emit(c, node, ancestors)...)
		}
		ancestors = append(ancestors, node)
		return true
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return specs, nil
}

func (t *Tree) fileOf(pkg *packages.Package, pos token.Pos) (*ast.File, string, error) {
	for _, file := range pkg.Syntax {
		if file.FileStart <= pos && pos < file.FileEnd {
			return file, pkg.Fset.Position(file.Pos()).Filename, nil
		}
	}
	return nil, "", fmt.Errorf("no syntax file for position")
}
