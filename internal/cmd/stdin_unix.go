//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func readStdinContext(ctx context.Context) ([]byte, error) {
	fd, err := unix.Dup(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)

	var out bytes.Buffer
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP}}
		if _, err := unix.Poll(poll, 50); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, err
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return nil, fmt.Errorf("read stdin: poll event %#x", poll[0].Revents)
		}
		if poll[0].Revents&(unix.POLLIN|unix.POLLHUP) == 0 {
			continue
		}
		n, err := unix.Read(fd, buffer)
		out.Write(buffer[:n])
		switch {
		case n == 0 && err == nil:
			return out.Bytes(), ctx.Err()
		case err == nil:
		case errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR):
		default:
			return nil, err
		}
	}
}
