package lib

type number int
type text string

type numeric interface {
	~int | ~float64
}

func ArithmeticDefined(a, b number) number {
	return (a + b) - (a*b)/(a%b)
}

func ArithmeticFloat(a, b float64) float64 {
	return (a + b) - (a*b)/b
}

func ArithmeticComplex(a, b complex128) complex128 {
	return (a + b) - (a*b)/b
}

func ArithmeticGeneric[T numeric](a, b T) T {
	return (a + b) - (a*b)/b
}

func RemainderGeneric[T ~int | ~int64](a, b T) T {
	return a % b
}

func MixedAddition[T ~int | ~string](a, b T) T {
	return a + b
}

func StringAddition(a, b string) string {
	return a + b
}

func DefinedStringAddition(a, b text) text {
	return a + b
}
