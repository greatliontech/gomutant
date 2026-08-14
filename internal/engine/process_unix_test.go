//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandContextKillsProcessGroup(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	cmd := commandContext(ctx, "sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("child process did not start")
	}
	cancel()
	started := time.Now()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled process group exited successfully")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("process-group cleanup took %s", elapsed)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d survived cancellation", childPID)
}

// An oracle process tree runs at low scheduling priority
// (REQ-exec-oracle-parallelism): the group's niceness is raised right
// after the spawn, before the oracle's real work begins. The sleep
// outwaits the parent's start-to-Setpriority window.
func TestOracleRunsAtLowPriority(t *testing.T) {
	if own := processNiceness(t, os.Getpid()); own >= oracleNiceness {
		t.Skipf("already running at niceness %d; lowering to %d needs privileges", own, oracleNiceness)
	}
	var out bytes.Buffer
	cmd := commandContext(context.Background(), "sh", "-c", "sleep 2; ps -o nice= -p $$")
	cmd.Stdout = &out
	if err := runOracleProcess(cmd); err != nil {
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
