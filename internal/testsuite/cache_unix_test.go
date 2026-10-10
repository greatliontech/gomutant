//go:build unix

package testsuite

import (
	"context"
	"errors"
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

func TestBuildCacheProbeContainsItsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\nsleep 30 &\necho $! > \"$GOMUTANT_CACHE_CHILD_PID\"\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOMUTANT_CACHE_CHILD_PID", pidFile)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resolveBuildCache(ctx, gotool.Runner{Containment: &gotool.Containment{}})
		done <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	pid := 0
	for pid == 0 {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if pid != 0 {
			break
		}
		select {
		case <-tick.C:
		case err := <-done:
			t.Fatalf("wrapper ended before child: %v", err)
		case <-deadline.C:
			t.Fatal("wrapper never published its child")
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled query succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cache query outlived cancellation")
	}
	// A zombie has exited and holds no pipe. A live descendant after the
	// parent's cancellation would reveal a command-only kill.
	for until := time.Now().Add(time.Second); ; {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		state := strings.TrimSpace(string(out))
		if err != nil {
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 1 {
				t.Fatalf("cannot inspect owned child: %v", err)
			}
			break
		}
		if state == "" || strings.HasPrefix(state, "Z") {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("descendant still live after cancellation: %s", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
