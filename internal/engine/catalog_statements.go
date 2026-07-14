package engine

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

func emitStatementLists(c *catalog, node ast.Node, ancestors []ast.Node) []candidateSpec {
	switch list := node.(type) {
	case *ast.BlockStmt:
		if blockContainsClauses(list, ancestors) {
			return nil
		}
		specs := statementDeletionCandidates(c, list.List)
		if blockCanBeEmptied(list, ancestors) {
			specs = append(specs, candidateSpec{
				operator: "block: empty", start: list.Lbrace, end: list.Rbrace + 1,
				position: list.Lbrace, family: 29, variant: 1,
				edits: []sourceEdit{c.edit(list.Lbrace+1, list.Rbrace, nil)},
			})
		}
		return specs
	case *ast.CaseClause:
		return clauseCandidates(c, list.Colon, list.Body)
	case *ast.CommClause:
		return clauseCandidates(c, list.Colon, list.Body)
	default:
		return nil
	}
}

func blockContainsClauses(block *ast.BlockStmt, ancestors []ast.Node) bool {
	if len(ancestors) == 0 {
		return false
	}
	switch parent := ancestors[len(ancestors)-1].(type) {
	case *ast.SwitchStmt:
		return parent.Body == block
	case *ast.TypeSwitchStmt:
		return parent.Body == block
	case *ast.SelectStmt:
		return parent.Body == block
	default:
		return false
	}
}

func blockCanBeEmptied(block *ast.BlockStmt, ancestors []ast.Node) bool {
	if len(ancestors) == 0 {
		return false
	}
	parent, ok := ancestors[len(ancestors)-1].(*ast.IfStmt)
	return ok && (parent.Body == block || parent.Else == block)
}

func clauseCandidates(c *catalog, colon token.Pos, statements []ast.Stmt) []candidateSpec {
	specs := statementDeletionCandidates(c, statements)
	end := colon + 1
	editStart, editEnd := end, end
	if len(statements) != 0 {
		end = statements[len(statements)-1].End()
		editStart, editEnd = statements[0].Pos(), end
	}
	specs = append(specs, candidateSpec{
		operator: "block: empty", start: colon, end: end,
		position: colon, family: 29, variant: 1,
		edits: []sourceEdit{c.edit(editStart, editEnd, nil)},
	})
	return specs
}

func statementDeletionCandidates(c *catalog, statements []ast.Stmt) []candidateSpec {
	var specs []candidateSpec
	for _, statement := range statements {
		switch statement.(type) {
		case *ast.DeclStmt, *ast.AssignStmt:
			continue
		}
		specs = append(specs, candidateSpec{
			operator: "statement: delete", start: statement.Pos(), end: statement.End(),
			family: 30, variant: 1, edits: []sourceEdit{c.deletionEdit(statement.Pos(), statement.End())},
		})
	}
	return specs
}

func emitAssignmentDrop(c *catalog, node ast.Node, ancestors []ast.Node) []candidateSpec {
	assignment, ok := node.(*ast.AssignStmt)
	if !ok || assignment.Tok == token.DEFINE || !assignmentDropContext(assignment, ancestors) {
		return nil
	}
	lhs := strings.Repeat("_, ", len(assignment.Lhs)-1) + "_"
	tokenEnd := assignment.TokPos + token.Pos(len(assignment.Tok.String()))
	replacement := append([]byte(lhs+" ="), c.text(tokenEnd, assignment.End())...)
	return []candidateSpec{{
		operator: "assignment: drop store", start: assignment.Pos(), end: assignment.End(),
		family: 31, variant: 1, edits: []sourceEdit{c.edit(assignment.Pos(), assignment.End(), replacement)},
	}}
}

func assignmentDropContext(assignment *ast.AssignStmt, ancestors []ast.Node) bool {
	if len(ancestors) == 0 {
		return false
	}
	switch parent := ancestors[len(ancestors)-1].(type) {
	case *ast.BlockStmt:
		return slices.Contains(parent.List, ast.Stmt(assignment))
	case *ast.CaseClause:
		return slices.Contains(parent.Body, ast.Stmt(assignment))
	case *ast.CommClause:
		return slices.Contains(parent.Body, ast.Stmt(assignment))
	case *ast.IfStmt:
		return parent.Init == assignment
	case *ast.ForStmt:
		return parent.Init == assignment || parent.Post == assignment
	case *ast.SwitchStmt:
		return parent.Init == assignment
	case *ast.TypeSwitchStmt:
		return parent.Init == assignment
	default:
		return false
	}
}
