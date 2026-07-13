//go:build windows

package cmd

import (
	"context"
	"io"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func readStdinContext(ctx context.Context) ([]byte, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	err := windows.DuplicateHandle(process, windows.Handle(os.Stdin.Fd()), process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return nil, err
	}
	input := os.NewFile(uintptr(duplicate), "gomutant-stdin")
	defer input.Close()
	type result struct {
		data []byte
		err  error
	}
	type readerState struct {
		thread windows.Handle
		err    error
	}
	ready := make(chan readerState, 1)
	finished := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
		ready <- readerState{thread: thread, err: err}
		if err != nil {
			finished <- result{err: err}
			return
		}
		data, err := io.ReadAll(input)
		finished <- result{data: data, err: err}
	}()
	state := <-ready
	if state.err != nil {
		<-finished
		return nil, state.err
	}
	defer windows.CloseHandle(state.thread)
	select {
	case result := <-finished:
		return result.data, result.err
	case <-ctx.Done():
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		cancelSynchronousIO.Call(uintptr(state.thread))
		select {
		case <-finished:
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
