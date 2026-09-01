package cmd

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// sigtermDrainDeadline bounds a SIGTERM-initiated drain: a supervisor
// that sends SIGTERM follows with SIGKILL on its own clock, and
// SIGKILL bypasses context cancellation entirely — in-flight oracle
// process trees would survive as orphans and per-process scratch
// would never sweep. The deadline is derived from the ecosystem's
// smallest common kill window (docker stop defaults to 10s;
// Kubernetes 30s; systemd 90s): 5 seconds drains what a short-oracle
// campaign can bank and hard-cancels — processes reaped, scratch
// swept — before ANY common supervisor escalates. An interactive
// SIGINT keeps the unbounded patient drain: the human at the terminal
// escalates by pressing Ctrl-C again. A var for deadline-injection in
// tests.
var sigtermDrainDeadline = 5 * time.Second

// softInterrupt is the interrupt policy the command tree runs under —
// SIGINT and SIGTERM alike: the first signal invokes an armed drain
// hook (the run verb's graceful stop) or, with none armed, cancels
// outright; a second signal of either kind always cancels hard. A
// SIGINT drain is bounded only by the in-flight oracles' own budgets
// (the human escalates); a SIGTERM drain additionally arms
// sigtermDrainDeadline, so a supervisor's stop banks what fits its
// kill window and then dies CLEANLY instead of eating a SIGKILL with
// orphaned process trees (REQ-exec-cancellation's graceful-interrupt
// clause).
type softInterrupt struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	drain  func()
	fired  bool
	// deadline is the armed SIGTERM drain bound; disarm stops it so a
	// completed drain's final merge is never shot mid-write.
	deadline *time.Timer
}

type softInterruptKey struct{}

// withSoftInterrupt installs the policy on ctx: SIGINT and SIGTERM
// route through the policy value instead of a direct context cancel.
// The returned stop releases the signal registration and cancels the
// derived context.
func withSoftInterrupt(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	s := &softInterrupt{cancel: cancel}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				if !s.fire(sig == syscall.SIGTERM) {
					return
				}
			case <-done:
				return
			}
		}
	}()
	return context.WithValue(ctx, softInterruptKey{}, s), func() {
		signal.Stop(ch)
		close(done)
		s.stopDeadline()
		cancel()
	}
}

// fire handles one delivered signal: the first with a drain armed
// drains — a SIGTERM drain additionally arms the deadline that
// hard-cancels if the drain outlasts a supervisor's kill window —
// everything else cancels hard. Returns false once the hard cancel
// ran (the watcher retires).
func (s *softInterrupt) fire(term bool) bool {
	s.mu.Lock()
	drain, fired := s.drain, s.fired
	s.fired = true
	if drain != nil && !fired && term {
		s.deadline = time.AfterFunc(sigtermDrainDeadline, s.cancel)
	}
	s.mu.Unlock()
	if drain != nil && !fired {
		drain()
		return true
	}
	s.stopDeadline()
	s.cancel()
	return false
}

// arm registers the drain hook consumed by the next first interrupt;
// disarm removes it and stops any armed SIGTERM deadline — the run
// verb disarms after the drain's banking completed, so the deadline
// never shoots the final merge. Arming after an interrupt already
// fired changes nothing — the next interrupt cancels hard.
func (s *softInterrupt) arm(drain func()) {
	s.mu.Lock()
	s.drain = drain
	s.mu.Unlock()
}

func (s *softInterrupt) disarm() {
	s.mu.Lock()
	s.drain = nil
	s.mu.Unlock()
	s.stopDeadline()
}

func (s *softInterrupt) stopDeadline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deadline != nil {
		s.deadline.Stop()
		s.deadline = nil
	}
}

// softInterruptFrom returns the installed policy, or nil outside the
// CLI (library embedding, MCP, tests) where SoftStop stays unset.
func softInterruptFrom(ctx context.Context) *softInterrupt {
	s, _ := ctx.Value(softInterruptKey{}).(*softInterrupt)
	return s
}
