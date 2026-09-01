package cmd

import (
	"context"
	"testing"
	"time"
)

// The two-stage SIGINT policy: with a drain armed the first interrupt
// drains without cancelling and a second cancels hard even though the
// drain stays armed; with none armed the first interrupt cancels —
// the unchanged semantics of every other verb
// (REQ-exec-cancellation's graceful-interrupt clause).
func TestSoftInterruptTwoStagePolicy(t *testing.T) {
	newPolicy := func() (*softInterrupt, context.Context) {
		ctx, cancel := context.WithCancel(context.Background())
		return &softInterrupt{cancel: cancel}, ctx
	}

	s, ctx := newPolicy()
	drained := 0
	s.arm(func() { drained++ })
	if !s.fire(false) {
		t.Fatal("armed first interrupt retired the watcher")
	}
	if drained != 1 || ctx.Err() != nil {
		t.Fatalf("armed first interrupt: drained=%d ctxErr=%v, want a drain without cancellation", drained, ctx.Err())
	}
	if s.fire(false) {
		t.Fatal("second interrupt did not retire the watcher")
	}
	if drained != 1 || ctx.Err() == nil {
		t.Fatalf("second interrupt: drained=%d ctxErr=%v, want the hard cancel with no second drain", drained, ctx.Err())
	}

	s, ctx = newPolicy()
	if s.fire(false) {
		t.Fatal("unarmed interrupt did not retire the watcher")
	}
	if ctx.Err() == nil {
		t.Fatal("unarmed first interrupt did not cancel")
	}

	// A disarmed policy (the run verb returned) cancels like unarmed.
	s, ctx = newPolicy()
	s.arm(func() { t.Fatal("disarmed drain invoked") })
	s.disarm()
	if s.fire(false) {
		t.Fatal("disarmed interrupt did not retire the watcher")
	}
	if ctx.Err() == nil {
		t.Fatal("disarmed interrupt did not cancel")
	}
}

// The deadline asymmetry is the SIGINT/SIGTERM contract's core: a
// SIGINT drain is bounded only by the in-flight oracles' own budgets
// — the human at the terminal escalates — while a SIGTERM drain arms
// the deadline (REQ-exec-cancellation's graceful-interrupt clause).
// Both halves pinned by VALUE against an injected deadline: dropping
// the term gate would silently deadline-bound the interactive drain
// and discard the window an operator expected to commit.
func TestDrainDeadlineArmsOnSIGTERMOnly(t *testing.T) {
	oldDeadline := sigtermDrainDeadline
	sigtermDrainDeadline = 100 * time.Millisecond
	t.Cleanup(func() { sigtermDrainDeadline = oldDeadline })

	newPolicy := func() (*softInterrupt, context.Context) {
		ctx, cancel := context.WithCancel(context.Background())
		return &softInterrupt{cancel: cancel}, ctx
	}

	s, ctx := newPolicy()
	s.arm(func() {})
	if !s.fire(false) {
		t.Fatal("armed interactive drain retired the watcher")
	}
	time.Sleep(3 * sigtermDrainDeadline)
	if ctx.Err() != nil {
		t.Fatal("an interactive (SIGINT) drain was deadline-bounded — the human escalates, no timer may")
	}

	s, ctx = newPolicy()
	s.arm(func() {})
	if !s.fire(true) {
		t.Fatal("armed SIGTERM drain retired the watcher")
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a SIGTERM drain was not deadline-bounded")
	}
}
