//go:build windows

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCommandContextKillsWindowsJob(t *testing.T) {
	pidFile := t.TempDir() + `\child.pid`
	ctx, cancel := context.WithCancel(context.Background())
	cmd := commandContext(ctx, os.Args[0], "-test.run=^TestWindowsJobHelper$")
	cmd.Env = append(os.Environ(), "GOMUTANT_WINDOWS_JOB_HELPER=parent", "GOMUTANT_WINDOWS_JOB_PIDFILE="+pidFile)
	runErr := make(chan error, 1)
	go func() { runErr <- cmd.Run() }()
	deadline := time.Now().Add(10 * time.Second)
	var childPID uint64
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		_ = windows.TerminateJobObject(cmd.job, 1)
		<-runErr
		t.Fatal("child process did not start")
	}
	cancel()
	if err := <-runErr; err == nil {
		t.Fatal("cancelled process job exited successfully")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(childPID))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return
		}
		if err != nil {
			t.Fatalf("inspect child process: %v", err)
		}
		var exitCode uint32
		err = windows.GetExitCodeProcess(process, &exitCode)
		windows.CloseHandle(process)
		if err != nil {
			t.Fatal(err)
		}
		if exitCode != 259 { // STILL_ACTIVE
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d survived cancellation", childPID)
}

func TestWindowsJobHelper(t *testing.T) {
	switch os.Getenv("GOMUTANT_WINDOWS_JOB_HELPER") {
	case "":
		return
	case "child":
		time.Sleep(time.Minute)
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsJobHelper$")
		child.Env = append(os.Environ(), "GOMUTANT_WINDOWS_JOB_HELPER=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("GOMUTANT_WINDOWS_JOB_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
			_ = child.Process.Kill()
			os.Exit(2)
		}
		_ = child.Wait()
	default:
		os.Exit(2)
	}
}
