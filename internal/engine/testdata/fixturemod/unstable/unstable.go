// Package unstable is the harness-instability fixture: its test
// binary's TestMain carries the count-drift marker machinery, isolated
// here so the lib fixture's test-main flow stays clean enough for
// observation proofs.
package unstable

func Add(a, b int) int { return a + b }
