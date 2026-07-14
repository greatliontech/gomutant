package engine

import (
	"go/ast"
	"go/types"
)

type returnVariant struct {
	operator    string
	replacement string
	family      int
	untyped     types.Type
}

func emitReturnSubstitution(c *catalog, node ast.Node, ancestors []ast.Node) []candidateSpec {
	statement, ok := node.(*ast.ReturnStmt)
	if !ok || len(statement.Results) == 0 {
		return nil
	}
	signature := enclosingReturnSignature(c, ancestors)
	if signature == nil || signature.Results().Len() != len(statement.Results) {
		return nil
	}
	var specs []candidateSpec
	for i, result := range statement.Results {
		for _, variant := range returnVariants(signature.Results().At(i).Type()) {
			specs = append(specs, candidateSpec{
				operator: variant.operator,
				start:    result.Pos(), end: result.End(), family: variant.family, variant: 1, index: i,
				edits: []sourceEdit{c.edit(result.Pos(), result.End(), []byte(variant.replacement))},
			})
		}
	}
	return specs
}

func enclosingReturnSignature(c *catalog, ancestors []ast.Node) *types.Signature {
	for i := len(ancestors) - 1; i >= 0; i-- {
		if function, ok := ancestors[i].(*ast.FuncLit); ok {
			signature, _ := c.pkg.TypesInfo.TypeOf(function).(*types.Signature)
			return signature
		}
	}
	object := c.pkg.TypesInfo.Defs[c.fd.Name]
	if object == nil {
		return nil
	}
	signature, _ := object.Type().(*types.Signature)
	return signature
}

func returnVariants(result types.Type) []returnVariant {
	result = types.Unalias(result)
	if _, ok := result.(*types.TypeParam); ok {
		return nil
	}
	var variants []returnVariant
	switch underlying := result.Underlying().(type) {
	case *types.Basic:
		switch {
		case underlying.Info()&types.IsBoolean != 0:
			variants = []returnVariant{
				{operator: "return: false", replacement: "false", family: 32, untyped: types.Typ[types.UntypedBool]},
				{operator: "return: true", replacement: "true", family: 33, untyped: types.Typ[types.UntypedBool]},
			}
		case underlying.Info()&types.IsNumeric != 0:
			variants = []returnVariant{{operator: "return: zero", replacement: "0", family: 34, untyped: types.Typ[types.UntypedInt]}}
		case underlying.Info()&types.IsString != 0:
			variants = []returnVariant{{operator: "return: zero", replacement: `""`, family: 34, untyped: types.Typ[types.UntypedString]}}
		case underlying.Kind() == types.UnsafePointer:
			variants = []returnVariant{{operator: "return: nil", replacement: "nil", family: 35, untyped: types.Typ[types.UntypedNil]}}
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		variants = []returnVariant{{operator: "return: nil", replacement: "nil", family: 35, untyped: types.Typ[types.UntypedNil]}}
	}
	kept := variants[:0]
	for _, variant := range variants {
		if types.AssignableTo(variant.untyped, result) {
			kept = append(kept, variant)
		}
	}
	return kept
}
