package engine

import (
	"go/ast"
	"go/scanner"
	"go/token"
)

func emitBooleanOperand(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok || expression.Op != token.LAND && expression.Op != token.LOR {
		return nil
	}
	forced, operator, family := "true", "boolean operand: -> true", 23
	if expression.Op == token.LOR {
		forced, operator, family = "false", "boolean operand: -> false", 24
	}
	operands := []ast.Expr{expression.X, expression.Y}
	specs := make([]candidateSpec, 0, len(operands))
	for i, operand := range operands {
		specs = append(specs, candidateSpec{
			operator: operator, start: operand.Pos(), end: operand.End(),
			family: family, variant: 1, index: i,
			edits: []sourceEdit{c.edit(operand.Pos(), operand.End(), []byte(forced))},
		})
	}
	return specs
}

func emitConditions(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	switch statement := node.(type) {
	case *ast.IfStmt:
		return conditionCandidates(c, statement.Cond, true)
	case *ast.ForStmt:
		if statement.Cond != nil {
			return conditionCandidates(c, statement.Cond, false)
		}
		end := statement.For + token.Pos(len(token.FOR.String()))
		return []candidateSpec{{
			operator: "condition: force false", start: statement.For, end: end,
			position: statement.For, family: 27, variant: 1,
			edits:                     []sourceEdit{conditionlessForEdit(c, statement)},
			preservesImportReferences: true,
		}}
	}
	return nil
}

func conditionCandidates(c *catalog, condition ast.Expr, allowTrue bool) []candidateSpec {
	replacement := append([]byte("!("), c.text(condition.Pos(), condition.End())...)
	replacement = append(replacement, ')')
	specs := []candidateSpec{{
		operator: "condition: negate", start: condition.Pos(), end: condition.End(),
		family: 25, variant: 1, edits: []sourceEdit{c.edit(condition.Pos(), condition.End(), replacement)},
		preservesImportReferences: true,
	}}
	if allowTrue {
		specs = append(specs, candidateSpec{
			operator: "condition: force true", start: condition.Pos(), end: condition.End(),
			family: 26, variant: 1, edits: []sourceEdit{c.edit(condition.Pos(), condition.End(), []byte("true"))},
		})
	}
	specs = append(specs, candidateSpec{
		operator: "condition: force false", start: condition.Pos(), end: condition.End(),
		family: 27, variant: 1, edits: []sourceEdit{c.edit(condition.Pos(), condition.End(), []byte("false"))},
	})
	return specs
}

func conditionlessForEdit(c *catalog, statement *ast.ForStmt) sourceEdit {
	start := c.tokens.Offset(statement.For + token.Pos(len(token.FOR.String())))
	body := c.tokens.Offset(statement.Body.Lbrace)
	header := c.source[start:body]
	files := token.NewFileSet()
	file := files.AddFile("for-header", -1, len(header))
	var lexer scanner.Scanner
	lexer.Init(file, header, nil, scanner.ScanComments)
	depth := 0
	for {
		position, symbol, literal := lexer.Scan()
		if symbol == token.EOF {
			break
		}
		switch symbol {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		}
		if symbol == token.SEMICOLON && literal == ";" && depth == 0 {
			start += file.Offset(position) + 1
			break
		}
	}
	return sourceEdit{start: start, end: start, replacement: []byte(" false")}
}

func emitLoopControl(c *catalog, node ast.Node, ancestors []ast.Node) []candidateSpec {
	branch, ok := node.(*ast.BranchStmt)
	if !ok || branch.Tok != token.BREAK && branch.Tok != token.CONTINUE {
		return nil
	}
	swapped, variant := token.BREAK, 2
	if branch.Tok == token.BREAK {
		if !hasContinueTarget(branch.Label, ancestors) {
			return nil
		}
		swapped, variant = token.CONTINUE, 1
	}
	end := branch.TokPos + token.Pos(len(branch.Tok.String()))
	return []candidateSpec{{
		operator: "loop control: " + branch.Tok.String() + " -> " + swapped.String(),
		start:    branch.TokPos, end: end, family: 14, variant: variant,
		edits:                     []sourceEdit{c.edit(branch.TokPos, end, []byte(swapped.String()))},
		preservesImportReferences: true,
	}}
}

func hasContinueTarget(label *ast.Ident, ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		if _, boundary := ancestors[i].(*ast.FuncLit); boundary {
			return false
		}
		if label == nil {
			switch ancestors[i].(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				return true
			}
			continue
		}
		labeled, ok := ancestors[i].(*ast.LabeledStmt)
		if !ok || labeled.Label.Name != label.Name {
			continue
		}
		switch labeled.Stmt.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		default:
			return false
		}
	}
	return false
}

func emitRangeSuppression(c *catalog, node ast.Node, _ []ast.Node) []candidateSpec {
	statement, ok := node.(*ast.RangeStmt)
	if !ok {
		return nil
	}
	insert := c.tokens.Offset(statement.Body.Lbrace) + 1
	return []candidateSpec{{
		operator: "range body: prepend break", start: statement.Pos(), end: statement.End(),
		position: statement.Range, family: 28, variant: 1,
		edits:                     []sourceEdit{{start: insert, end: insert, replacement: []byte("\nbreak")}},
		preservesImportReferences: true,
	}}
}
