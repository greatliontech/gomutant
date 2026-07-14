package engine

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

type unaryVariant struct {
	replacement string
	rank        int
}

var unaryVariants = map[token.Token]unaryVariant{
	token.ADD: {replacement: "-", rank: 1},
	token.SUB: {replacement: "+", rank: 2},
	token.NOT: {rank: 3},
	token.XOR: {rank: 4},
}

type compoundVariant struct {
	family      string
	familyRank  int
	variantRank int
	replacement token.Token
	storeRank   int
}

var compoundVariants = map[token.Token]compoundVariant{
	token.ADD_ASSIGN:     {family: "compound arithmetic", familyRank: 9, variantRank: 1, replacement: token.SUB_ASSIGN, storeRank: 1},
	token.SUB_ASSIGN:     {family: "compound arithmetic", familyRank: 9, variantRank: 2, replacement: token.ADD_ASSIGN, storeRank: 2},
	token.MUL_ASSIGN:     {family: "compound arithmetic", familyRank: 9, variantRank: 3, replacement: token.QUO_ASSIGN, storeRank: 3},
	token.QUO_ASSIGN:     {family: "compound arithmetic", familyRank: 9, variantRank: 4, replacement: token.MUL_ASSIGN, storeRank: 4},
	token.REM_ASSIGN:     {family: "compound arithmetic", familyRank: 9, variantRank: 5, replacement: token.MUL_ASSIGN, storeRank: 5},
	token.AND_ASSIGN:     {family: "compound bitwise", familyRank: 10, variantRank: 1, replacement: token.OR_ASSIGN, storeRank: 6},
	token.OR_ASSIGN:      {family: "compound bitwise", familyRank: 10, variantRank: 2, replacement: token.AND_ASSIGN, storeRank: 7},
	token.XOR_ASSIGN:     {family: "compound bitwise", familyRank: 10, variantRank: 3, replacement: token.AND_ASSIGN, storeRank: 8},
	token.AND_NOT_ASSIGN: {family: "compound bitwise", familyRank: 10, variantRank: 4, replacement: token.AND_ASSIGN, storeRank: 9},
	token.SHL_ASSIGN:     {family: "compound shift", familyRank: 11, variantRank: 1, replacement: token.SHR_ASSIGN, storeRank: 10},
	token.SHR_ASSIGN:     {family: "compound shift", familyRank: 11, variantRank: 2, replacement: token.SHL_ASSIGN, storeRank: 11},
}

func emitUnary(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	expression, ok := node.(*ast.UnaryExpr)
	if !ok {
		return nil
	}
	variant, ok := unaryVariants[expression.Op]
	if !ok {
		return nil
	}
	end := expression.OpPos + token.Pos(len(expression.Op.String()))
	replacement := variant.replacement
	operatorReplacement := replacement
	if replacement == "" {
		operatorReplacement = "identity"
	}
	return []candidateSpec{{
		operator: fmt.Sprintf("unary: %s -> %s", expression.Op, operatorReplacement),
		start:    expression.Pos(), end: expression.End(), position: expression.OpPos, family: 8, variant: variant.rank,
		edits:                     []sourceEdit{c.edit(expression.OpPos, end, []byte(replacement))},
		preservesImportReferences: true,
	}}
}

func emitCompoundAssignment(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	assignment, ok := node.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return nil
	}
	variant, ok := compoundVariants[assignment.Tok]
	if !ok {
		return nil
	}
	end := assignment.TokPos + token.Pos(len(assignment.Tok.String()))
	var specs []candidateSpec
	if compoundMappingApplicable(c, assignment) {
		specs = append(specs, candidateSpec{
			operator: fmt.Sprintf("%s: %s -> %s", variant.family, assignment.Tok, variant.replacement),
			start:    assignment.TokPos, end: end, family: variant.familyRank, variant: variant.variantRank,
			edits:                     []sourceEdit{c.edit(assignment.TokPos, end, []byte(variant.replacement.String()))},
			preservesImportReferences: true,
		})
	}
	if assignmentTokenApplicable(c, assignment, token.ASSIGN) {
		specs = append(specs, candidateSpec{
			operator: fmt.Sprintf("compound store: %s -> =", assignment.Tok),
			start:    assignment.TokPos, end: end, family: 12, variant: variant.storeRank,
			edits:                     []sourceEdit{c.edit(assignment.TokPos, end, []byte("="))},
			preservesImportReferences: true,
		})
	}
	return specs
}

func compoundMappingApplicable(c *catalog, assignment *ast.AssignStmt) bool {
	if assignment.Tok != token.ADD_ASSIGN {
		return true
	}
	probe := &ast.BinaryExpr{X: assignment.Lhs[0], OpPos: assignment.TokPos, Op: token.SUB, Y: assignment.Rhs[0]}
	return types.CheckExpr(c.pkg.Fset, c.pkg.Types, assignment.Pos(), probe, nil) == nil
}

func assignmentTokenApplicable(c *catalog, assignment *ast.AssignStmt, replacement token.Token) bool {
	probeAssignment := &ast.AssignStmt{
		Lhs:    assignment.Lhs,
		TokPos: assignment.TokPos,
		Tok:    replacement,
		Rhs:    assignment.Rhs,
	}
	probe := &ast.FuncLit{
		Type: &ast.FuncType{Func: assignment.Pos(), Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{Lbrace: assignment.Pos(), List: []ast.Stmt{probeAssignment}, Rbrace: assignment.End()},
	}
	return types.CheckExpr(c.pkg.Fset, c.pkg.Types, assignment.Pos(), probe, nil) == nil
}

func emitIncDec(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	statement, ok := node.(*ast.IncDecStmt)
	if !ok {
		return nil
	}
	swapped, variant := token.DEC, 1
	if statement.Tok == token.DEC {
		swapped, variant = token.INC, 2
	}
	end := statement.TokPos + token.Pos(len(statement.Tok.String()))
	return []candidateSpec{{
		operator: fmt.Sprintf("increment/decrement: %s -> %s", statement.Tok, swapped),
		start:    statement.TokPos, end: end, family: 13, variant: variant,
		edits:                     []sourceEdit{c.edit(statement.TokPos, end, []byte(swapped.String()))},
		preservesImportReferences: true,
	}}
}
