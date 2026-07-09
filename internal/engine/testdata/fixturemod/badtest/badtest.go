// Package badtest declares a Test-named function whose signature go test
// rejects: it can never run, so it must never enter a derived oracle. It
// lives alone because go test refuses the whole package.
package badtest

func B() int { return 1 }
