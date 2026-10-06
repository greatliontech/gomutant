package mcpserver

import (
	"runtime/debug"
	"time"
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

// callBegan records a tool call's start: the pending idle release is
// cancelled while any call is in flight.
func (s *Server) callBegan() {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	s.inFlight++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
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

// releaseHeap returns freed heap to the host now, instead of on the
// scavenger's own schedule; the observer names the server so a test
// counts its own server's returns alone.
func (s *Server) releaseHeap() {
	debug.FreeOSMemory()
	if seams.heapReleased != nil {
		seams.heapReleased(s)
	}
}
