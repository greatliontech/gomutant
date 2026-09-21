//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

// TestOracleSpawnRidesTheContainedRunner pins the oracle spawn to the
// tree's runner and its containment: the prepared command is the go
// command in the given directory under the policy's environment (PWD
// derived from the directory), leading its own process group, with a
// cancellation hook and the policy's wait delay — and no quit grace,
// so a bound's expiry kills the group outright and reads as the kill
// (REQ-exec-cancellation, REQ-exec-oracle-parallelism). The sweep of
// the group itself is gofresh's contract, pinned there (gotool's
// TestContainSweepsTheGroupOnCancellation).
func TestOracleSpawnRidesTheContainedRunner(t *testing.T) {
	dir := t.TempDir()
	cmd, err := oracleCommand(context.Background(), dir, GoEnv(dir), "version")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cmd.Path) != "go" || len(cmd.Args) != 2 || cmd.Args[1] != "version" || cmd.Dir != dir {
		t.Fatalf("prepared %v in %q, want `go version` in %q", cmd.Args, cmd.Dir, dir)
	}
	if pwd, ok := gotool.LookupEnv(cmd.Env, "PWD"); !ok || pwd != dir {
		t.Fatalf("PWD = %q/%v, want the policy's derivation from the directory", pwd, ok)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %+v, want the child leading its own process group", cmd.SysProcAttr)
	}
	if cmd.Cancel == nil || cmd.WaitDelay != gotool.DefaultWaitDelay {
		t.Fatalf("cancel %v, wait delay %s, want the containment's hook and the policy's delay", cmd.Cancel != nil, cmd.WaitDelay)
	}
	if c := goRunner.Containment; c == nil || c.Quit != nil || c.Grace != 0 || c.WaitDelay != 0 {
		t.Fatalf("containment %+v, want the policy's defaults with no quit grace", c)
	}
	if _, err := oracleCommand(context.Background(), dir, nil, "version"); err == nil {
		t.Fatal("a nil environment prepared a spawn; the policy names the environment it runs under")
	}
}

// An oracle process tree runs at low scheduling priority
// (REQ-exec-oracle-parallelism): the group's niceness is raised right
// after the spawn, before the oracle's real work begins. The sleep
// outwaits the parent's start-to-Setpriority window. The command is
// built by hand in its own process group — the runner's containment
// gives a real oracle the group (TestOracleSpawnRidesTheContainedRunner).
func TestOracleRunsAtLowPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a child process")
	}
	if own := processNiceness(t, os.Getpid()); own >= oracleNiceness {
		t.Skipf("already running at niceness %d; lowering to %d needs privileges", own, oracleNiceness)
	}
	var out bytes.Buffer
	cmd := exec.Command("sh", "-c", "sleep 2; ps -o nice= -p $$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = &out
	if err := runOracleProcess(cmd, OracleBounds{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != strconv.Itoa(oracleNiceness) {
		t.Fatalf("oracle niceness = %q, want %d", got, oracleNiceness)
	}
}

// processNiceness reads a process's niceness via ps - the portable
// user-facing scale, where raw getpriority is kernel-scaled on Linux.
func processNiceness(t *testing.T, pid int) int {
	out, err := exec.Command("ps", "-o", "nice=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("read niceness of %d: %v", pid, err)
	}
	nice, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse niceness %q: %v", out, err)
	}
	return nice
}

// The timeout attribution needs a process the bound KILLED (ExitCode
// -1: never exited on its own), not merely a failed one under the
// bound's cause: the timer can fire during post-exit teardown (scratch
// sweep, observation finalization), and relabeling a self-exited
// process — a clean pass scored as a timeout kill, or a
// test-attributed failure scored as "(timeout)" and dodging the
// oracle-set gate — fabricates evidence. The cancellation twin scores
// a completed measurement from its output even when the context died
// in teardown, and discards only genuinely failed runs
// (REQ-exec-attribution's cause discrimination).
func TestOracleBudgetFiredDiscriminates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs child processes for real exit states")
	}
	exited := exec.Command("sh", "-c", "true")
	_ = exited.Run()
	failed := exec.Command("sh", "-c", "exit 1")
	failedErr := failed.Run()
	killed := exec.Command("sh", "-c", "kill -KILL $$")
	killedErr := killed.Run()
	if exited.ProcessState == nil || failed.ProcessState == nil || killed.ProcessState == nil || failedErr == nil || killedErr == nil {
		t.Fatal("fixture processes did not produce the three exit states")
	}
	// The platform-owned killed fact: only the signal-killed process
	// reads as "did not exit on its own".
	if !oracleProcessKilled(killed) || oracleProcessKilled(exited) || oracleProcessKilled(failed) {
		t.Fatalf("oracleProcessKilled = killed:%v exited:%v failed:%v, want true/false/false", oracleProcessKilled(killed), oracleProcessKilled(exited), oracleProcessKilled(failed))
	}
	budget, cancel := context.WithTimeoutCause(context.Background(), -time.Second, errOracleBudgetExceeded)
	defer cancel()
	<-budget.Done()
	if !oracleBudgetFired(killedErr, killed.ProcessState, oracleProcessKilled(killed), budget) {
		t.Fatal("signal-killed process under the fired bound did not attribute as a timeout")
	}
	if oracleBudgetFired(nil, exited.ProcessState, oracleProcessKilled(exited), budget) {
		t.Fatal("cleanly exited process attributed as a timeout — teardown expiry fabricates a kill")
	}
	if oracleBudgetFired(failedErr, failed.ProcessState, oracleProcessKilled(failed), budget) {
		t.Fatal("self-exited failing process attributed as a timeout — a test-attributed kill relabeled \"(timeout)\" dodges the oracle-set gate")
	}
	// Never-started splits on EVIDENCE: Start returning the context's
	// own expiry is the bound preventing the start (the sub-startup
	// explicit timeout); any other start failure is environmental
	// noise even when the bound's cause is set by check time.
	if !oracleBudgetFired(fmt.Errorf("starting oracle: %w", context.DeadlineExceeded), nil, false, budget) {
		t.Fatal("bound-prevented start did not attribute — a sub-startup explicit timeout must refuse as the bound firing")
	}
	if oracleBudgetFired(errors.New("fork/exec /usr/bin/go: resource temporarily unavailable"), nil, false, budget) {
		t.Fatal("fork-level start failure attributed as a timeout — environmental noise scored as a kill")
	}
	parent, pcancel := context.WithTimeout(context.Background(), -time.Second)
	defer pcancel()
	child, ccancel := context.WithTimeoutCause(parent, time.Hour, errOracleBudgetExceeded)
	defer ccancel()
	<-child.Done()
	if oracleBudgetFired(killedErr, killed.ProcessState, oracleProcessKilled(killed), child) {
		t.Fatal("parent expiry attributed as the oracle bound firing")
	}

	if !oracleRunCancelled(killedErr, child) {
		t.Fatal("failed run under a dead context did not discard as cancelled")
	}
	if oracleRunCancelled(nil, child) {
		t.Fatal("completed measurement discarded as cancelled — a teardown-window expiry threw away sound evidence")
	}
	live := context.Background()
	if oracleRunCancelled(killedErr, live) {
		t.Fatal("failed run under a live context discarded as cancelled instead of being scored")
	}
}
