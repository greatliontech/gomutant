package engine

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

var activeCandidateEmitters = []candidateEmitter{
	emitGo4Binary,
	emitGo4Literal,
	emitGo4Branch,
	emitGo4IncDec,
	emitGo4AssignmentSwap,
	emitGo4Condition,
	emitGo4Block,
	emitGo4Return,
}

var comparisonSwap = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL,
	token.LSS: token.GEQ, token.GEQ: token.LSS,
	token.GTR: token.LEQ, token.LEQ: token.GTR,
	token.LAND: token.LOR, token.LOR: token.LAND,
}

var arithmeticSwap = map[token.Token]token.Token{
	token.ADD: token.SUB, token.SUB: token.ADD,
	token.MUL: token.QUO, token.QUO: token.MUL,
}

var assignArithmeticSwap = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN,
	token.MUL_ASSIGN: token.QUO_ASSIGN, token.QUO_ASSIGN: token.MUL_ASSIGN,
}

func emitGo4Binary(c *catalog, node ast.Node) []candidateSpec {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	var specs []candidateSpec
	if swapped, ok := comparisonSwap[expression.Op]; ok {
		family := 3
		if expression.Op == token.EQL || expression.Op == token.NEQ {
			family = 1
		} else if expression.Op == token.LAND || expression.Op == token.LOR {
			family = 4
		}
		end := expression.OpPos + token.Pos(len(expression.Op.String()))
		specs = append(specs, candidateSpec{operator: fmt.Sprintf("%s -> %s", expression.Op, swapped), start: expression.OpPos, end: end, family: family, variant: 1, edits: []sourceEdit{c.edit(expression.OpPos, end, []byte(swapped.String()))}, preservesImportReferences: true})
	}
	if swapped, ok := arithmeticSwap[expression.Op]; ok && numeric(c, expression.X) {
		end := expression.OpPos + token.Pos(len(expression.Op.String()))
		specs = append(specs, candidateSpec{operator: fmt.Sprintf("%s -> %s", expression.Op, swapped), start: expression.OpPos, end: end, family: 5, variant: 1, edits: []sourceEdit{c.edit(expression.OpPos, end, []byte(swapped.String()))}, preservesImportReferences: true})
	}
	if expression.Op == token.LAND || expression.Op == token.LOR {
		forced := "true"
		if expression.Op == token.LOR {
			forced = "false"
		}
		for i, operand := range []ast.Expr{expression.X, expression.Y} {
			specs = append(specs, candidateSpec{operator: "force " + forced, start: operand.Pos(), end: operand.End(), family: 23, variant: 1, index: i, edits: []sourceEdit{c.edit(operand.Pos(), operand.End(), []byte(forced))}})
		}
	}
	return specs
}

func emitGo4Literal(c *catalog, node ast.Node) []candidateSpec {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return nil
	}
	return []candidateSpec{{operator: "increment literal", start: literal.Pos(), end: literal.End(), family: 15, variant: 1, edits: []sourceEdit{c.edit(literal.Pos(), literal.End(), []byte(incrementInt(literal.Value)))}, preservesImportReferences: true}}
}

func emitGo4Branch(c *catalog, node ast.Node) []candidateSpec {
	branch, ok := node.(*ast.BranchStmt)
	if !ok || branch.Tok != token.BREAK && branch.Tok != token.CONTINUE {
		return nil
	}
	swapped := token.CONTINUE
	if branch.Tok == token.CONTINUE {
		swapped = token.BREAK
	}
	end := branch.TokPos + token.Pos(len(branch.Tok.String()))
	return []candidateSpec{{operator: fmt.Sprintf("%s -> %s", branch.Tok, swapped), start: branch.TokPos, end: end, family: 14, variant: 1, edits: []sourceEdit{c.edit(branch.TokPos, end, []byte(swapped.String()))}, preservesImportReferences: true}}
}

func emitGo4IncDec(c *catalog, node ast.Node) []candidateSpec {
	statement, ok := node.(*ast.IncDecStmt)
	if !ok {
		return nil
	}
	swapped := token.DEC
	if statement.Tok == token.DEC {
		swapped = token.INC
	}
	end := statement.TokPos + token.Pos(len(statement.Tok.String()))
	return []candidateSpec{{operator: fmt.Sprintf("%s -> %s", statement.Tok, swapped), start: statement.TokPos, end: end, family: 13, variant: 1, edits: []sourceEdit{c.edit(statement.TokPos, end, []byte(swapped.String()))}, preservesImportReferences: true}}
}

func emitGo4AssignmentSwap(c *catalog, node ast.Node) []candidateSpec {
	assignment, ok := node.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) == 0 || !numeric(c, assignment.Lhs[0]) {
		return nil
	}
	swapped, ok := assignArithmeticSwap[assignment.Tok]
	if !ok {
		return nil
	}
	end := assignment.TokPos + token.Pos(len(assignment.Tok.String()))
	return []candidateSpec{{operator: fmt.Sprintf("%s -> %s", assignment.Tok, swapped), start: assignment.TokPos, end: end, family: 9, variant: 1, edits: []sourceEdit{c.edit(assignment.TokPos, end, []byte(swapped.String()))}, preservesImportReferences: true}}
}

func emitGo4Condition(c *catalog, node ast.Node) []candidateSpec {
	var condition ast.Expr
	switch statement := node.(type) {
	case *ast.IfStmt:
		condition = statement.Cond
	case *ast.ForStmt:
		condition = statement.Cond
	}
	if condition == nil {
		return nil
	}
	replacement := append([]byte("!("), c.text(condition.Pos(), condition.End())...)
	replacement = append(replacement, ')')
	return []candidateSpec{{operator: "negate condition", start: condition.Pos(), end: condition.End(), family: 25, variant: 1, edits: []sourceEdit{c.edit(condition.Pos(), condition.End(), replacement)}, preservesImportReferences: true}}
}

func emitGo4Block(c *catalog, node ast.Node) []candidateSpec {
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

func emitGo4Return(c *catalog, node ast.Node) []candidateSpec {
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
