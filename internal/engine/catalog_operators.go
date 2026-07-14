package engine

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

var activeCandidateEmitters = []candidateEmitter{
	emitComparison,
	emitArithmeticBinary,
	emitBitwiseBinary,
	emitUnary,
	emitCompoundAssignment,
	emitBooleanOperand,
	emitIntegerLiteral,
	emitLoopControl,
	emitIncDec,
	emitConditions,
	emitRangeSuppression,
	emitBlockMutations,
	emitZeroReturn,
}

func emitIntegerLiteral(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return nil
	}
	return []candidateSpec{{operator: "increment literal", start: literal.Pos(), end: literal.End(), family: 15, variant: 1, edits: []sourceEdit{c.edit(literal.Pos(), literal.End(), []byte(incrementInt(literal.Value)))}, preservesImportReferences: true}}
}

func emitBlockMutations(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	block, ok := node.(*ast.BlockStmt)
	if !ok {
		return nil
	}
	var specs []candidateSpec
	for _, statement := range block.List {
		switch assignment := statement.(type) {
		case *ast.ExprStmt, *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt:
			specs = append(specs, candidateSpec{operator: "delete statement", start: statement.Pos(), end: statement.End(), family: 30, variant: 1, edits: []sourceEdit{c.deletionEdit(statement.Pos(), statement.End())}})
		case *ast.AssignStmt:
			if assignment.Tok == token.DEFINE {
				continue
			}
			lhs := strings.Repeat("_, ", len(assignment.Lhs)-1) + "_"
			tokenEnd := assignment.TokPos + token.Pos(len(assignment.Tok.String()))
			replacement := append([]byte(lhs+" ="), c.text(tokenEnd, assignment.End())...)
			specs = append(specs, candidateSpec{operator: "drop assignment", start: assignment.Pos(), end: assignment.End(), family: 31, variant: 1, edits: []sourceEdit{c.edit(assignment.Pos(), assignment.End(), replacement)}})
		}
	}
	return specs
}

func emitZeroReturn(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	statement, ok := node.(*ast.ReturnStmt)
	if !ok {
		return nil
	}
	var specs []candidateSpec
	for i, result := range statement.Results {
		replacement := zeroReplacement(c.pkg.TypesInfo.TypeOf(result))
		if replacement == nil {
			continue
		}
		specs = append(specs, candidateSpec{operator: "zero return", start: result.Pos(), end: result.End(), family: 32, variant: 1, index: i, edits: []sourceEdit{c.edit(result.Pos(), result.End(), replacement)}})
	}
	return specs
}

func numeric(c *catalog, expression ast.Expr) bool {
	basic, ok := c.pkg.TypesInfo.TypeOf(expression).(*types.Basic)
	return ok && basic.Info()&types.IsNumeric != 0
}

func incrementInt(literal string) string {
	n, err := strconv.ParseUint(literal, 0, 63)
	if err != nil {
		return literal
	}
	return strconv.FormatUint(n+1, 10)
}

func zeroReplacement(typ types.Type) []byte {
	switch value := typ.(type) {
	case *types.Basic:
		switch info := value.Info(); {
		case info&types.IsBoolean != 0:
			return []byte("false")
		case info&types.IsNumeric != 0:
			return []byte("0")
		case info&types.IsString != 0:
			return []byte(`""`)
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return []byte("nil")
	case *types.Named:
		if _, ok := value.Underlying().(*types.Interface); ok {
			return []byte("nil")
		}
	}
	return nil
}
