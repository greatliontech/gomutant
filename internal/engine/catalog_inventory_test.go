package engine

import (
	"context"
	"maps"
	"testing"
)

func TestComprehensiveCatalogInventory(t *testing.T) {
	tr := fixtureTree(t)
	symbols := []string{
		"MappingEQ", "MappingNEQ", "MappingLT", "MappingLE", "MappingGT", "MappingGE", "MappingAND", "MappingOR",
		"ArithmeticAdd", "ArithmeticSub", "ArithmeticMul", "ArithmeticDiv", "ArithmeticRem",
		"BitwiseAnd", "BitwiseOr", "BitwiseXor", "BitwiseClear", "ShiftLeft", "ShiftRight",
		"UnaryPlus", "UnaryMinus", "UnaryNot", "UnaryXor",
		"CompoundAdd", "CompoundSub", "CompoundMul", "CompoundDiv", "CompoundRem", "CompoundAnd", "CompoundOr", "CompoundXor", "CompoundClear", "CompoundShiftLeft", "CompoundShiftRight", "Increment", "Decrement",
		"BreakMapping", "ContinueMapping", "IfCondition", "MappingAND", "MappingOR", "RangeOnce",
		"LiteralInteger", "LiteralRune", "LiteralFloat", "LiteralImaginary", "LiteralTrue", "LiteralFalse", "LiteralNonempty", "LiteralEmpty",
		"StatementBlocks", "StatementKinds", "StatementDropStores",
		"ReturnBoolean", "ReturnNumber", "ReturnPointer",
	}
	got := map[string]int{}
	for _, symbol := range symbols {
		catalog, err := tr.candidateCatalog(context.Background(), "example.com/fixture/lib."+symbol)
		if err != nil {
			t.Fatal(err)
		}
		specs, err := catalog.enumerate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range specs {
			if rank, ok := got[spec.operator]; ok && rank != spec.family {
				t.Fatalf("operator %q has family ranks %d and %d", spec.operator, rank, spec.family)
			}
			got[spec.operator] = spec.family
		}
	}
	want := map[string]int{
		"equality: == -> !=": 1, "equality: != -> ==": 1,
		"relational boundary: < -> <=": 2, "relational boundary: <= -> <": 2, "relational boundary: > -> >=": 2, "relational boundary: >= -> >": 2,
		"relational negation: < -> >=": 3, "relational negation: <= -> >": 3, "relational negation: > -> <=": 3, "relational negation: >= -> <": 3,
		"logical: && -> ||": 4, "logical: || -> &&": 4,
		"arithmetic: + -> -": 5, "arithmetic: - -> +": 5, "arithmetic: * -> /": 5, "arithmetic: / -> *": 5, "arithmetic: % -> *": 5,
		"bitwise: & -> |": 6, "bitwise: | -> &": 6, "bitwise: ^ -> &": 6, "bitwise: &^ -> &": 6,
		"shift: << -> >>": 7, "shift: >> -> <<": 7,
		"unary: + -> -": 8, "unary: - -> +": 8, "unary: ! -> identity": 8, "unary: ^ -> identity": 8,
		"compound arithmetic: += -> -=": 9, "compound arithmetic: -= -> +=": 9, "compound arithmetic: *= -> /=": 9, "compound arithmetic: /= -> *=": 9, "compound arithmetic: %= -> *=": 9,
		"compound bitwise: &= -> |=": 10, "compound bitwise: |= -> &=": 10, "compound bitwise: ^= -> &=": 10, "compound bitwise: &^= -> &=": 10,
		"compound shift: <<= -> >>=": 11, "compound shift: >>= -> <<=": 11,
		"compound store: += -> =": 12, "compound store: -= -> =": 12, "compound store: *= -> =": 12, "compound store: /= -> =": 12, "compound store: %= -> =": 12,
		"compound store: &= -> =": 12, "compound store: |= -> =": 12, "compound store: ^= -> =": 12, "compound store: &^= -> =": 12, "compound store: <<= -> =": 12, "compound store: >>= -> =": 12,
		"increment/decrement: ++ -> --": 13, "increment/decrement: -- -> ++": 13,
		"loop control: break -> continue": 14, "loop control: continue -> break": 14,
		"integer literal: magnitude +1": 15, "rune literal: value +1": 16, "float literal: value +1": 17, "imaginary literal: value +1": 18,
		"boolean literal: true -> false": 19, "boolean literal: false -> true": 20, "string literal: nonempty -> empty": 21, "string literal: empty -> nonempty": 22,
		"boolean operand: -> true": 23, "boolean operand: -> false": 24,
		"condition: negate": 25, "condition: force true": 26, "condition: force false": 27,
		"range body: prepend break": 28, "block: empty": 29, "statement: delete": 30, "assignment: drop store": 31,
		"return: false": 32, "return: true": 33, "return: zero": 34, "return: nil": 35,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("catalog inventory = %v, want %v", got, want)
	}
}
