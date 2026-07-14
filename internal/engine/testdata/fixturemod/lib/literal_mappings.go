package lib

func LiteralInteger() int          { return 0x0f }
func LiteralRune() rune            { return '\x61' }
func LiteralFloat() float64        { return 1e2 }
func LiteralImaginary() complex128 { return 2i }
func LiteralTrue() bool            { return true }
func LiteralFalse() bool           { return false }
func LiteralNonempty() string      { return `value` }
func LiteralEmpty() string         { return "" }
