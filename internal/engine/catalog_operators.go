package engine

import (
	"go/ast"
	"go/token"
	"strings"
)

var activeCandidateEmitters = []candidateEmitter{
	emitComparison,
	emitArithmeticBinary,
	emitBitwiseBinary,
	emitUnary,
	emitCompoundAssignment,
	emitBooleanOperand,
	emitScalarValue,
	emitLoopControl,
	emitIncDec,
	emitConditions,
	emitRangeSuppression,
	emitBlockMutations,
	emitReturnSubstitution,
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
