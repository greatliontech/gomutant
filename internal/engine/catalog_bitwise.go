package engine

import (
	"fmt"
	"go/ast"
	"go/token"
)

type bitwiseVariant struct {
	family      string
	familyRank  int
	variantRank int
	replacement token.Token
}

var bitwiseVariants = map[token.Token]bitwiseVariant{
	token.AND:     {family: "bitwise", familyRank: 6, variantRank: 1, replacement: token.OR},
	token.OR:      {family: "bitwise", familyRank: 6, variantRank: 2, replacement: token.AND},
	token.XOR:     {family: "bitwise", familyRank: 6, variantRank: 3, replacement: token.AND},
	token.AND_NOT: {family: "bitwise", familyRank: 6, variantRank: 4, replacement: token.AND},
	token.SHL:     {family: "shift", familyRank: 7, variantRank: 1, replacement: token.SHR},
	token.SHR:     {family: "shift", familyRank: 7, variantRank: 2, replacement: token.SHL},
}

func emitBitwiseBinary(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	variant, ok := bitwiseVariants[expression.Op]
	if !ok {
		return nil
	}
	end := expression.OpPos + token.Pos(len(expression.Op.String()))
	return []candidateSpec{{
		operator: fmt.Sprintf("%s: %s -> %s", variant.family, expression.Op, variant.replacement),
		start:    expression.OpPos, end: end, family: variant.familyRank, variant: variant.variantRank,
		edits:                     []sourceEdit{c.edit(expression.OpPos, end, []byte(variant.replacement.String()))},
		preservesImportReferences: true,
	}}
}
