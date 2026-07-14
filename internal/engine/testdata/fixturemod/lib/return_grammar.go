package lib

import "unsafe"

type ReturnBool bool
type ReturnInt int
type ReturnFloat float64
type ReturnComplex complex128
type ReturnText string
type ReturnPointerType *int
type ReturnSlice []int
type ReturnMap map[string]int
type ReturnChan chan int
type ReturnFunc func()
type ReturnInterface interface{ Marker() }
type ReturnUnsafe unsafe.Pointer
type ReturnIntAlias = int
type ReturnPointerAlias = *int
type ReturnGenericAlias[T any] = *T

func ReturnDefined(b ReturnBool, i ReturnInt, f ReturnFloat, c ReturnComplex, s ReturnText, p ReturnPointerType) (ReturnBool, ReturnInt, ReturnFloat, ReturnComplex, ReturnText, ReturnPointerType) {
	return b, i, f, c, s, p
}

func ReturnAliases(i ReturnIntAlias, p ReturnPointerAlias) (ReturnIntAlias, ReturnPointerAlias) {
	return i, p
}

func ReturnInstantiatedAlias(value ReturnGenericAlias[int]) ReturnGenericAlias[int] { return value }

func ReturnNilDomains(pointer *int, slice []int, mapping map[string]int, channel chan int, function func(), value any, unsafePointer unsafe.Pointer) (*int, []int, map[string]int, chan int, func(), any, unsafe.Pointer) {
	return pointer, slice, mapping, channel, function, value, unsafePointer
}

func ReturnDefinedNilDomains(slice ReturnSlice, mapping ReturnMap, channel ReturnChan, function ReturnFunc, value ReturnInterface, unsafePointer ReturnUnsafe) (ReturnSlice, ReturnMap, ReturnChan, ReturnFunc, ReturnInterface, ReturnUnsafe) {
	return slice, mapping, channel, function, value, unsafePointer
}

func ReturnMultiple(flag bool, number int, pointer *int) (bool, int, *int) {
	return flag, number, pointer
}

func ReturnDeclaredInterface() any { return 1 }

func ReturnArray(value [1]int) [1]int { return value }

type ReturnStructValue struct{ Value int }

func ReturnStruct(value ReturnStructValue) ReturnStructValue { return value }

func ReturnTypeParameter[T ~int](value T) T { return value }

func ReturnBare() (value int) { return }

func returnTuple() (bool, int) { return false, 0 }

func ReturnTuple() (bool, int) { return returnTuple() }

func ReturnNested() int {
	nested := func() bool { return true }
	_ = nested
	return 1
}

func ReturnDeepNested() int {
	nested := func() string {
		deeper := func() bool { return true }
		_ = deeper
		return "value"
	}
	_ = nested
	return 1
}

func ReturnGrouped(value int) (left, right int) { return value, value }

func returnOne() int { return 1 }

func ReturnSingleCall() int { return returnOne() }

func ReturnParenthesized(value int) int { return (value) }

type ReturnReceiver struct{}

func (ReturnReceiver) Value(value bool) bool { return value }
