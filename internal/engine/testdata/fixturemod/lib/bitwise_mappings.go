package lib

func BitwiseAnd(a, b int) int   { return a & b }
func BitwiseOr(a, b int) int    { return a | b }
func BitwiseXor(a, b int) int   { return a ^ b }
func BitwiseClear(a, b int) int { return a &^ b }
func ShiftLeft(a, b uint) uint  { return a << b }
func ShiftRight(a, b uint) uint { return a >> b }
