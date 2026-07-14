package lib

func UnaryOverflow() int8 {
	const value int8 = -128
	return value
}

func CompoundDivideByZero(value int) int {
	value *= 0
	return value
}
