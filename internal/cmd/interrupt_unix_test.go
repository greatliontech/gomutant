//go:build unix

package cmd

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// SIGTERM rides the SAME two-stage policy as SIGINT — this pins the
// signal REGISTRATION by delivering real SIGTERMs to the test
// process — and its drain is DEADLINE-BOUNDED: a supervisor's stop
// banks what fits its kill window, then hard-cancels cleanly instead
// of eating a SIGKILL with orphaned oracle process trees
// (REQ-exec-cancellation's graceful-interrupt clause). A completed
// drain disarms the deadline, so the final merge is never shot
// mid-write; a second signal of EITHER kind cancels hard.
func TestSIGTERMJoinsGracefulPolicy(t *testing.T) {
	oldDeadline := sigtermDrainDeadline
	sigtermDrainDeadline = 300 * time.Millisecond
	t.Cleanup(func() { sigtermDrainDeadline = oldDeadline })

	// First SIGTERM drains; the deadline then hard-cancels a drain
	// that never completes.
	ctx, stop := withSoftInterrupt(context.Background())
	s := softInterruptFrom(ctx)
	drained := make(chan struct{}, 1)
	s.arm(func() { drained <- struct{}{} })
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("first SIGTERM did not reach the armed drain")
	}
	if ctx.Err() != nil {
		t.Fatal("first SIGTERM cancelled instead of draining")
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the SIGTERM drain deadline did not hard-cancel a stuck drain")
	}
	stop()

	// A drain that completes (the run verb disarms) stops the
	// deadline: no late cancellation shoots the final merge.
	ctx, stop = withSoftInterrupt(context.Background())
	s = softInterruptFrom(ctx)
	s.arm(func() { drained <- struct{}{} })
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("first SIGTERM did not reach the armed drain")
	}
	s.disarm()
	select {
	case <-ctx.Done():
		t.Fatal("the deadline fired after the drain completed and disarmed")
	case <-time.After(2 * sigtermDrainDeadline):
	}

	// The second stage is signal-agnostic: SIGTERM then SIGINT
	// cancels hard through one channel.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a SIGINT after a SIGTERM drain did not cancel hard")
	}
	stop()
}
