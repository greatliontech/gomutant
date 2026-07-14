package lib

type orderedString string
type flag bool

func EqualGeneric[T comparable](a, b T) bool {
	return a == b || a != b
}

func RelationsGeneric[T ~int | ~string](a, b T) bool {
	return a < b || a <= b || a > b || a >= b
}

func RelationsDefined(a, b orderedString) bool {
	return a < b
}

func Logical(a, b bool) bool {
	return a && b || a || b
}

func LogicalDefined(a, b flag) flag {
	return a && b || a || b
}

func LogicalGeneric[T ~bool](a, b T) T {
	return a && b || a || b
}

func EqualityLogical(a, b int, enabled bool) bool {
	return a == b && enabled
}

func EqualityConcrete(s, t string, b, c bool, p, q *int, x, y any) bool {
	return s == t || s != t || b == c || p == q || p != nil || x == y || x == nil
}

func RelationsString(s, t string) bool {
	return s < t || s <= t || s > t || s >= t
}
