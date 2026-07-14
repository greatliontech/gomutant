package lib

func MappingEQ(a, b int) bool   { return a == b }
func MappingNEQ(a, b int) bool  { return a != b }
func MappingLT(a, b int) bool   { return a < b }
func MappingLE(a, b int) bool   { return a <= b }
func MappingGT(a, b int) bool   { return a > b }
func MappingGE(a, b int) bool   { return a >= b }
func MappingAND(a, b bool) bool { return a && b }
func MappingOR(a, b bool) bool  { return a || b }
