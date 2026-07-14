package lib

type bits uint
type bitsAlias = uint

type integerBits interface {
	~int | ~uint64
}

func BitwiseDefined(a, b bits) bits {
	return (a & b) | (a^b)&^b
}

func BitwiseGeneric[T integerBits](a, b T) T {
	return (a & b) | (a^b)&^b
}

func BitwiseConstants() int {
	const value = (1 & 2) | (3^4)&^5
	return value
}

func BitwiseAlias(a, b bitsAlias) bitsAlias {
	return a & b
}

func ShiftDefined(value bits, amount uint) bits {
	return (value << amount) >> amount
}

func ShiftGeneric[T integerBits, U ~uint](value T, amount U) T {
	return (value << amount) >> amount
}

func ShiftConstants() int {
	const value = (1 << 2) >> 1
	return value
}
