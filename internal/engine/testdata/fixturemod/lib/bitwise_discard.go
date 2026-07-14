package lib

func BitwiseDuplicate(value int) int {
	switch value {
	case 0:
		return 0
	case 1 ^ 0:
		return 1
	}
	return 2
}

func ShiftOverflow() uint8 {
	const value uint8 = 1 >> 8
	return value
}
