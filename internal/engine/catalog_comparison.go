package engine

import (
	"fmt"
	"go/ast"
	"go/token"
)

type comparisonVariant struct {
	family      string
	familyRank  int
	variantRank int
	replacement token.Token
}

var comparisonVariants = map[token.Token][]comparisonVariant{
	token.EQL:  {{family: "equality", familyRank: 1, variantRank: 1, replacement: token.NEQ}},
	token.NEQ:  {{family: "equality", familyRank: 1, variantRank: 2, replacement: token.EQL}},
	token.LSS:  {{family: "relational boundary", familyRank: 2, variantRank: 1, replacement: token.LEQ}, {family: "relational negation", familyRank: 3, variantRank: 1, replacement: token.GEQ}},
	token.LEQ:  {{family: "relational boundary", familyRank: 2, variantRank: 2, replacement: token.LSS}, {family: "relational negation", familyRank: 3, variantRank: 2, replacement: token.GTR}},
	token.GTR:  {{family: "relational boundary", familyRank: 2, variantRank: 3, replacement: token.GEQ}, {family: "relational negation", familyRank: 3, variantRank: 3, replacement: token.LEQ}},
	token.GEQ:  {{family: "relational boundary", familyRank: 2, variantRank: 4, replacement: token.GTR}, {family: "relational negation", familyRank: 3, variantRank: 4, replacement: token.LSS}},
	token.LAND: {{family: "logical", familyRank: 4, variantRank: 1, replacement: token.LOR}},
	token.LOR:  {{family: "logical", familyRank: 4, variantRank: 2, replacement: token.LAND}},
}

func emitComparison(c *catalog, node ast.Node) []candidateSpec {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	variants := comparisonVariants[expression.Op]
	if len(variants) == 0 {
		return nil
	}
	end := expression.OpPos + token.Pos(len(expression.Op.String()))
	specs := make([]candidateSpec, 0, len(variants))
	for _, variant := range variants {
		specs = append(specs, candidateSpec{
			operator: fmt.Sprintf("%s: %s -> %s", variant.family, expression.Op, variant.replacement),
			start:    expression.OpPos, end: end,
			family: variant.familyRank, variant: variant.variantRank,
			edits:                     []sourceEdit{c.edit(expression.OpPos, end, []byte(variant.replacement.String()))},
			preservesImportReferences: true,
		})
	}
	return specs
}
