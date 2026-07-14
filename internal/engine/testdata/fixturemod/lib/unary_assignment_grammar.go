package lib

type UnaryNumber int
type UnaryFlag bool
type UnaryAlias = int

func UnaryDefined(number UnaryNumber, flag UnaryFlag) (UnaryNumber, UnaryNumber, UnaryNumber, UnaryFlag) {
	return +number, -number, ^number, !flag
}

func UnaryGenericNumeric[T ~int | ~float64](value T) (T, T) {
	return +value, -value
}

func UnaryGenericInteger[T ~int | ~uint](value T) T { return ^value }
func UnaryGenericBoolean[T ~bool](value T) T        { return !value }

func UnaryConstants() (int, int, int, bool) {
	const positive = +1
	const negative = -1
	const complement = ^1
	const negated = !false
	return positive, negative, complement, negated
}

func UnaryAliases(number UnaryAlias, flag bool) (UnaryAlias, UnaryAlias, UnaryAlias, bool) {
	return +number, -number, ^number, !flag
}

func UnaryExcluded(pointer *int, channel <-chan int) (*int, int, int) {
	value := 1
	return &value, *pointer, <-channel
}

type CompoundNumber int
type CompoundAlias = int

func CompoundDefined(value, right CompoundNumber) CompoundNumber {
	value += right
	value -= right
	value *= right
	value /= right
	value %= right
	value &= right
	value |= right
	value ^= right
	value &^= right
	value <<= right
	value >>= right
	return value
}

func CompoundGeneric[T ~int](value, right T) T {
	value += right
	value -= right
	value *= right
	value /= right
	value %= right
	value &= right
	value |= right
	value ^= right
	value &^= right
	value <<= right
	value >>= right
	return value
}

func CompoundAliases(value, right CompoundAlias) CompoundAlias {
	value += right
	value -= right
	value *= right
	value /= right
	value %= right
	value &= right
	value |= right
	value ^= right
	value &^= right
	value <<= right
	value >>= right
	return value
}

func CompoundMixedAddition[T ~int | ~string](value, right T) T {
	value += right
	return value
}

func CompoundShiftIncompatible(value uint, amount int) uint {
	value <<= amount
	return value
}

func CompoundShiftLarge(value uint8) uint8 {
	value <<= 300
	return value
}

type CompoundHolder struct {
	Value int
}

func CompoundPlaces(pointer *int, values []int, holder *CompoundHolder, index int) {
	*pointer += 1
	values[index] += 1
	holder.Value += 1
	holder.add(1)
}

func (holder *CompoundHolder) add(value int) {
	holder.Value += value
}
