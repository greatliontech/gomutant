package lib

func IntegerLiteralForms() {
	const binary = 0b1
	const octal = 0o7
	const legacyOctal = 077
	const hexadecimal = 0xff
	const separated = 1_000
	const huge = 184467440737095516160
	const negative = -1
	_, _, _, _, _, _ = binary, octal, legacyOctal, hexadecimal, separated, negative
	_ = huge == huge
}

func RuneLiteralForms() {
	const plain = 'a'
	const escaped = '\n'
	const hexadecimal = '\x41'
	const byteEscape = '\xff'
	const octalEscape = '\377'
	const runeError = '\uFFFD'
	const unicodeMaximum = '\U0010FFFF'
	const beforeSurrogate = '\uD7FF'
	_, _, _, _, _, _, _, _ = plain, escaped, hexadecimal, byteEscape, octalEscape, runeError, unicodeMaximum, beforeSurrogate
}

func FloatLiteralForms() {
	const decimal = 1.5
	const exponent = 1e2
	const hexadecimal = 0x1p2
	_, _, _ = decimal, exponent, hexadecimal
}

func ImaginaryLiteralForms() {
	const decimal = 2i
	const hexadecimal = 0x1p2i
	_, _ = decimal, hexadecimal
}

func BooleanLiteralForms() (bool, bool) { return true, false }

func ShadowedBooleanLiterals(true, false bool) (bool, bool) { return true, false }

type shadowedBooleanField struct {
	true bool
}

func ShadowedBooleanSelector(value shadowedBooleanField) bool { return value.true }

func StringLiteralForms() {
	type tagged struct {
		Value string `json:"value"`
	}
	const interpreted = "value"
	const raw = `value`
	const escaped = "\x00"
	const emptyInterpreted = ""
	const emptyRaw = ``
	_, _, _, _, _ = interpreted, raw, escaped, emptyInterpreted, emptyRaw
	_ = tagged{}
}
