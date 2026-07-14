package lib

func UnaryPlus(value int) int  { return +value }
func UnaryMinus(value int) int { return -value }
func UnaryNot(value bool) bool { return !value }
func UnaryXor(value int) int   { return ^value }
func CompoundAdd(value, right int) int {
	value += right
	return value
}
func CompoundSub(value, right int) int {
	value -= right
	return value
}
func CompoundMul(value, right int) int {
	value *= right
	return value
}
func CompoundDiv(value, right int) int {
	value /= right
	return value
}
func CompoundRem(value, right int) int {
	value %= right
	return value
}
func CompoundAnd(value, right int) int {
	value &= right
	return value
}
func CompoundOr(value, right int) int {
	value |= right
	return value
}
func CompoundXor(value, right int) int {
	value ^= right
	return value
}
func CompoundClear(value, right int) int {
	value &^= right
	return value
}
func CompoundShiftLeft(value, right int) int {
	value <<= right
	return value
}
func CompoundShiftRight(value, right int) int {
	value >>= right
	return value
}
func Increment(value int) int {
	value++
	return value
}
func Decrement(value int) int {
	value--
	return value
}
