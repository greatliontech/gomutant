package cmd

import (
	"context"
	"testing"
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
	if !s.fire() {
		t.Fatal("armed first interrupt retired the watcher")
	}
	if drained != 1 || ctx.Err() != nil {
		t.Fatalf("armed first interrupt: drained=%d ctxErr=%v, want a drain without cancellation", drained, ctx.Err())
	}
	if s.fire() {
		t.Fatal("second interrupt did not retire the watcher")
	}
	if drained != 1 || ctx.Err() == nil {
		t.Fatalf("second interrupt: drained=%d ctxErr=%v, want the hard cancel with no second drain", drained, ctx.Err())
	}

	s, ctx = newPolicy()
	if s.fire() {
		t.Fatal("unarmed interrupt did not retire the watcher")
	}
	if ctx.Err() == nil {
		t.Fatal("unarmed first interrupt did not cancel")
	}

	// A disarmed policy (the run verb returned) cancels like unarmed.
	s, ctx = newPolicy()
	s.arm(func() { t.Fatal("disarmed drain invoked") })
	s.disarm()
	if s.fire() {
		t.Fatal("disarmed interrupt did not retire the watcher")
	}
	if ctx.Err() == nil {
		t.Fatal("disarmed interrupt did not cancel")
	}
}
