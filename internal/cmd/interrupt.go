package cmd

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

// softInterrupt is the SIGINT policy the command tree runs under: the
// first interrupt invokes an armed drain hook (the run verb's graceful
// stop) or, with none armed, cancels outright; a second interrupt
// always cancels hard. SIGTERM stays the process-level hard cancel
// (cmd/gomutant wires it to the root context), so unattended
// environments keep the immediate-stop semantics
// (REQ-exec-cancellation's graceful-interrupt clause).
type softInterrupt struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	drain  func()
	fired  bool
}

type softInterruptKey struct{}

// withSoftInterrupt installs the policy on ctx: SIGINT routes through
// the policy value instead of a direct context cancel. The returned
// stop releases the signal registration and cancels the derived
// context.
func withSoftInterrupt(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	s := &softInterrupt{cancel: cancel}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				if !s.fire() {
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
		cancel()
	}
}

// fire handles one delivered interrupt: the first with a drain armed
// drains; everything else cancels hard. Returns false once the hard
// cancel ran (the watcher retires).
func (s *softInterrupt) fire() bool {
	s.mu.Lock()
	drain, fired := s.drain, s.fired
	s.fired = true
	s.mu.Unlock()
	if drain != nil && !fired {
		drain()
		return true
	}
	s.cancel()
	return false
}

// arm registers the drain hook consumed by the next first interrupt;
// disarm removes it. Arming after an interrupt already fired changes
// nothing — the next interrupt cancels hard.
func (s *softInterrupt) arm(drain func()) {
	s.mu.Lock()
	s.drain = drain
	s.mu.Unlock()
}

func (s *softInterrupt) disarm() {
	s.mu.Lock()
	s.drain = nil
	s.mu.Unlock()
}

// softInterruptFrom returns the installed policy, or nil outside the
// CLI (library embedding, MCP, tests) where SoftStop stays unset.
func softInterruptFrom(ctx context.Context) *softInterrupt {
	s, _ := ctx.Value(softInterruptKey{}).(*softInterrupt)
	return s
}
