package lib

type arithmeticAlias = int

type intersectedNumeric interface {
	~int | ~float64
	comparable
}

func ArithmeticAlias(a, b arithmeticAlias) arithmeticAlias {
	return a + b
}

func ArithmeticIntersected[T intersectedNumeric](a, b T) T {
	return (a + b) - (a*b)/b
}

func ArithmeticUntyped() int {
	const value = 1 + 2
	return value
}

func ArithmeticIota() int {
	const value = iota + 1
	return value
}

func ArithmeticImaginary() complex128 {
	const value = 1i + 2i
	return value
}
