package engine

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"unicode/utf8"
)

func emitScalarValue(c *catalog, node ast.Node, ancestors []ast.Node) []candidateSpec {
	if literal, ok := node.(*ast.BasicLit); ok {
		for i := len(ancestors) - 1; i >= 0; i-- {
			if field, ok := ancestors[i].(*ast.Field); ok && field.Tag == literal {
				return nil
			}
		}
	}
	start, end, operator, replacement, family, ok := scalarReplacement(c, node)
	if !ok {
		return nil
	}
	return []candidateSpec{{
		operator: operator,
		start:    start, end: end, family: family, variant: 1,
		edits:                     []sourceEdit{c.edit(start, end, []byte(replacement))},
		preservesImportReferences: true,
	}}
}

func scalarReplacement(c *catalog, node ast.Node) (token.Pos, token.Pos, string, string, int, bool) {
	switch value := node.(type) {
	case *ast.BasicLit:
		replacement, operator, family, ok := basicLiteralReplacement(value)
		return value.Pos(), value.End(), operator, replacement, family, ok
	case *ast.Ident:
		if value.Name != "true" && value.Name != "false" || c.pkg.TypesInfo.Uses[value] != types.Universe.Lookup(value.Name) {
			return 0, 0, "", "", 0, false
		}
		replacement, family := "true", 20
		if value.Name == "true" {
			replacement, family = "false", 19
		}
		return value.Pos(), value.End(), "boolean literal: " + value.Name + " -> " + replacement, replacement, family, true
	default:
		return 0, 0, "", "", 0, false
	}
}

func basicLiteralReplacement(literal *ast.BasicLit) (string, string, int, bool) {
	switch literal.Kind {
	case token.INT:
		value := constant.MakeFromLiteral(literal.Value, token.INT, 0)
		if value.Kind() != constant.Int {
			return "", "", 0, false
		}
		next := constant.BinaryOp(value, token.ADD, constant.MakeInt64(1))
		return next.ExactString(), "integer literal: magnitude +1", 15, true
	case token.CHAR:
		decoded, _, tail, err := strconv.UnquoteChar(literal.Value[1:len(literal.Value)-1], '\'')
		if err != nil || tail != "" {
			return "", "", 0, false
		}
		next := decoded + 1
		if !utf8.ValidRune(next) {
			return "", "", 0, false
		}
		return strconv.QuoteRune(next), "rune literal: value +1", 16, true
	case token.FLOAT:
		return "(" + literal.Value + " + 1.0)", "float literal: value +1", 17, true
	case token.IMAG:
		return "(" + literal.Value + " + 1i)", "imaginary literal: value +1", 18, true
	case token.STRING:
		decoded, err := strconv.Unquote(literal.Value)
		if err != nil {
			return "", "", 0, false
		}
		if decoded == "" {
			return `"mutant"`, "string literal: empty -> nonempty", 22, true
		}
		return `""`, "string literal: nonempty -> empty", 21, true
	default:
		return "", "", 0, false
	}
}
