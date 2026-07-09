// Package sub is a nested workspace member: a published module whose
// symbols and nested members must stay in scope.
package sub

// Nested is resolvable only if the engine walks workspace members.
func Nested() int { return 2 }
