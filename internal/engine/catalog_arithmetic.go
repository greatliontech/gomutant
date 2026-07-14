package engine

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

type arithmeticVariant struct {
	replacement token.Token
	rank        int
}

var arithmeticVariants = map[token.Token]arithmeticVariant{
	token.ADD: {replacement: token.SUB, rank: 1},
	token.SUB: {replacement: token.ADD, rank: 2},
	token.MUL: {replacement: token.QUO, rank: 3},
	token.QUO: {replacement: token.MUL, rank: 4},
	token.REM: {replacement: token.MUL, rank: 5},
}

func emitArithmeticBinary(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	variant, ok := arithmeticVariants[expression.Op]
	if !ok {
		return nil
	}
	if expression.Op == token.ADD && !additionSupportsSubtraction(c, expression, variant.replacement) {
		return nil
	}
	end := expression.OpPos + token.Pos(len(expression.Op.String()))
	return []candidateSpec{{
		operator: fmt.Sprintf("arithmetic: %s -> %s", expression.Op, variant.replacement),
		start:    expression.OpPos, end: end, family: 5, variant: variant.rank,
		edits:                     []sourceEdit{c.edit(expression.OpPos, end, []byte(variant.replacement.String()))},
		preservesImportReferences: true,
	}}
}

func additionSupportsSubtraction(c *catalog, expression *ast.BinaryExpr, replacement token.Token) bool {
	typ := types.Unalias(c.pkg.TypesInfo.TypeOf(expression))
	switch value := typ.(type) {
	case *types.Basic:
		return value.Info()&types.IsNumeric != 0
	case *types.Named:
		basic, ok := value.Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsNumeric != 0
	case *types.TypeParam:
		probe := &ast.BinaryExpr{X: expression.X, OpPos: expression.OpPos, Op: replacement, Y: expression.Y}
		return types.CheckExpr(c.pkg.Fset, c.pkg.Types, expression.Pos(), probe, nil) == nil
	default:
		return false
	}
}
