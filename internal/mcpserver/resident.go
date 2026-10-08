package mcpserver

import (
	"runtime/debug"
	"time"

	"github.com/greatliontech/gofresh/resident"
	gomutant "github.com/greatliontech/gomutant"
)

// The server's resident set is its working set, never its largest
// request's (REQ-mcp-resident-set): when the last in-flight tool call
// ends the freed heap is returned to the host, and the loaded tree
// cached across calls is released once the server has been idle for
// idleTreeRelease, so an idle server holds the runtime's floor alone.
// The process's own memory limit is the fleet rule's (gofresh/resident)
// — installed where the server and the commands start.

// idleTreeRelease is how long the server keeps its cached tree after
// the last call ends with none in flight: a burst of calls reuses one
// load; an idle server drops it (REQ-mcp-resident-set).
const idleTreeRelease = 60 * time.Second

// callBegan records a tool call's start: every call re-derives the
// fleet ceiling first — the fleet rule derives over the family's room,
// the family's own held set added back, so a derivation under an
// in-flight call counts nothing of that call's held set against the
// host; what the walk cannot see of the call's memory (its oracles'
// tmpfs work directories, the kernel's allocations on its behalf)
// reads as the host's and the room errs low, and a child caught
// between its clone and its exec shares the server's memory map and
// reads as a second copy of the server's held set, so the room can
// err high by that much until the next derivation (gofresh/resident's
// stated blind spots; a soft limit cuts nothing either way) — the
// call is COUNTED before the walk, under the server's lock, which
// guards only the counter and the idle timer: the pending idle
// release is cancelled while any call is in flight, and a call that
// begins inside the window keeps the tree whatever the walk takes
// (counted after it, the window could elapse during the walk and drop
// the tree the call then reloads). The walk runs past the lock; two
// calls beginning together walk twice and the last install wins — the
// two readings differ by what moved between them, milliseconds.
func (s *Server) callBegan() {
	s.idleMu.Lock()
	s.inFlight++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	s.idleMu.Unlock()
	installCeiling()
}

// callEnded records a tool call's end: when it was the last in flight
// the idle release is armed and a heap return begins — off the reply
// path, on its own goroutine, at most one in progress and one pending
// at any moment: a transition under a return in progress marks the
// pending one, run when the current completes with no call in flight,
// so ends under a return fold into it and a last end is always
// followed by a return; a serve that has ended arms nothing.
func (s *Server) callEnded() {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	s.inFlight--
	if s.inFlight > 0 || s.stopped {
		return
	}
	s.idle = time.AfterFunc(seams.idleRelease, s.releaseIdle)
	if s.returning {
		s.trailing = true
		return
	}
	s.returning = true
	go s.returnHeap()
}

// returnHeap runs the heap return in progress and the one trailing
// return a transition marked meanwhile, then clears the flag.
func (s *Server) returnHeap() {
	for {
		s.releaseHeap()
		s.idleMu.Lock()
		again := s.trailing && s.inFlight == 0 && !s.stopped
		s.trailing = false
		if !again {
			s.returning = false
		}
		s.idleMu.Unlock()
		if !again {
			return
		}
	}
}

// stopIdle ends the policy with the serve: the pending release is
// cancelled and no later call end arms one (a handler outliving the
// serve must not hold the server through a timer).
func (s *Server) stopIdle() {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	s.stopped = true
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
}

// releaseIdle drops the cached tree when the idle window elapsed with
// no call in flight — judged and done under idleMu, which every call's
// start and end take, so a call that begins meanwhile either stops the
// timer first or finds the tree already gone and reloads, never a tree
// dropped under a call that found it (the lock order is idleMu then
// mu: releaseIdle is the one site holding both, and loadTreeContext
// holds mu only inside a call, i.e. while inFlight > 0, which this
// judgment excludes) — then, past the lock so a call's start never
// waits on a collection, returns the heap the tree held.
func (s *Server) releaseIdle() {
	s.idleMu.Lock()
	if s.inFlight > 0 || s.stopped {
		s.idleMu.Unlock()
		return
	}
	s.mu.Lock()
	s.tree, s.treeKey = nil, ""
	s.mu.Unlock()
	s.idleMu.Unlock()
	s.releaseHeap()
	if seams.treeReleased != nil {
		seams.treeReleased(s)
	}
}

// residentSuffix is the process's reading as the heartbeat's tail, in
// the words the CLI's progress line carries (gomutant.ResidentReading
// over the seam's sampler, resident.Sample otherwise —
// REQ-exec-run-status); nothing where the host answers no reading.
func residentSuffix() string {
	sample := resident.Sample
	if seams.residentSample != nil {
		sample = seams.residentSample
	}
	return gomutant.ResidentTail(gomutant.ResidentReading(sample))
}

// installCeiling installs the fleet ceiling for the process — at serve
// start and at every tool call's start, so a long-lived server's
// ceiling rises or falls with the host (REQ-mcp-resident-set) — and
// reports it to the seam's observer.
func installCeiling() int64 {
	limit := resident.InstallCeiling()
	if seams.memoryLimitInstalled != nil {
		seams.memoryLimitInstalled(limit)
	}
	return limit
}

// releaseHeap returns freed heap to the host now, instead of on the
// scavenger's own schedule; the observer names the server so a test
// counts its own server's returns alone.
func (s *Server) releaseHeap() {
	debug.FreeOSMemory()
	if seams.heapReleased != nil {
		seams.heapReleased(s)
	}
}
