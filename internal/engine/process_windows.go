//go:build windows

package engine

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobCommand struct {
	*exec.Cmd
	ctx       context.Context
	job       windows.Handle
	jobErr    error
	mu        sync.Mutex
	assigned  bool
	cancelled bool
}

func commandContext(ctx context.Context, name string, args ...string) *jobCommand {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED}
	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	}
	wrapped := &jobCommand{Cmd: cmd, ctx: ctx, job: job, jobErr: err}
	cmd.Cancel = func() error {
		wrapped.mu.Lock()
		defer wrapped.mu.Unlock()
		wrapped.cancelled = true
		if wrapped.assigned {
			return windows.TerminateJobObject(wrapped.job, 1)
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	return wrapped
}

func (c *jobCommand) Run() error {
	if c.job != 0 {
		defer windows.CloseHandle(c.job)
	}
	if c.jobErr != nil {
		return fmt.Errorf("create process job: %w", c.jobErr)
	}
	if err := c.Start(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		_ = c.Wait()
		return c.ctx.Err()
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(c.job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		c.mu.Unlock()
		_ = c.Process.Kill()
		_ = c.Wait()
		return fmt.Errorf("assign process job: %w", err)
	}
	c.assigned = true
	c.mu.Unlock()
	if err := c.ctx.Err(); err != nil {
		_ = windows.TerminateJobObject(c.job, 1)
		_ = c.Wait()
		return err
	}
	if err := resumeProcess(uint32(c.Process.Pid)); err != nil {
		_ = windows.TerminateJobObject(c.job, 1)
		_ = c.Wait()
		return fmt.Errorf("resume process: %w", err)
	}
	return c.Wait()
}

func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, err = windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			return err
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return err
		}
	}
}
