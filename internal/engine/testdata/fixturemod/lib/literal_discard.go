package lib

func IntegerLiteralOverflow() uint8 {
	const value uint8 = 255
	return value
}

func IntegerLiteralDuplicate(value int) {
	switch value {
	case 0:
	case 1:
	}
}

func RuneLiteralDuplicate(value rune) {
	switch value {
	case 'a':
	case 'b':
	}
}

func FloatLiteralDuplicate(value float64) {
	switch value {
	case 1.0:
	case 2.0:
	}
}

func ImaginaryLiteralCases(value complex128) {
	switch value {
	case 1i:
	case 2i:
	}
}

func BooleanLiteralCases(value bool) {
	switch value {
	case true:
	case false:
	}
}

func StringLiteralDuplicate(value string) {
	switch value {
	case "mutant":
	case "":
	}
}
