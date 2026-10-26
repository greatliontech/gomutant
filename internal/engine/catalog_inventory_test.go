package engine

import (
	"context"
	"go/token"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/go-x-tools/go/packages"
)

func TestComprehensiveCatalogInventory(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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

func FuzzCandidateSpecOrderingAndPositionDisambiguation(f *testing.F) {
	f.Add(uint8(1), uint8(5), uint8(1), uint8(0), uint8(1), uint8(5), uint8(1), uint8(0), true)
	f.Add(uint8(2), uint8(8), uint8(3), uint8(1), uint8(1), uint8(4), uint8(2), uint8(0), false)
	f.Fuzz(func(t *testing.T, s1, e1, fam1, var1, s2, e2, fam2, var2 uint8, sameOp bool) {
		fset := token.NewFileSet()
		file := fset.AddFile("f.go", -1, 1024)
		pkg := &packages.Package{Fset: fset}
		startA := file.Pos(int(s1)%256 + 1)
		endA := file.Pos(int(s1)%256 + int(e1)%256 + 1)
		startB := file.Pos(int(s2)%256 + 1)
		endB := file.Pos(int(s2)%256 + int(e2)%256 + 1)
		opB := "op-b"
		if sameOp {
			opB = "op-a"
		}
		a := candidateSpec{operator: "op-a", start: startA, end: endA, position: startA, family: int(fam1), variant: int(var1), index: 0}
		b := candidateSpec{operator: opB, start: startB, end: endB, position: startB, family: int(fam2), variant: int(var2), index: 1}
		forward := []candidateSpec{a, b}
		reverse := []candidateSpec{b, a}
		orderCandidateSpecs(forward)
		orderCandidateSpecs(reverse)
		if !slices.EqualFunc(forward, reverse, func(x, y candidateSpec) bool { return x.index == y.index }) {
			t.Fatalf("orderCandidateSpecs not canonical across permutations: %+v vs %+v", forward, reverse)
		}
		if forward[1].less(forward[0]) {
			t.Fatalf("ordered specs inverted less: %+v", forward)
		}
		positions := candidatePositions(pkg, forward)
		if len(positions) != 2 {
			t.Fatalf("positions = %v", positions)
		}
		if forward[0].position == forward[1].position && forward[0].operator == forward[1].operator {
			if !strings.HasSuffix(positions[1], "#2") || positions[1] != positions[0]+"#2" {
				t.Fatalf("duplicate (position, operator) not disambiguated: %v", positions)
			}
		} else if strings.Contains(positions[0], "#") || strings.Contains(positions[1], "#") {
			t.Fatalf("distinct (position, operator) received occurrence suffix: %v", positions)
		}
	})
}

