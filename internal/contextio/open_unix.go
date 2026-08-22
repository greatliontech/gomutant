//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package contextio

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func openRegularRead(path string) (*os.File, error) {
	return openRegularUnix(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0, "read")
}

func openRegularWrite(path string, mode os.FileMode) (*os.File, error) {
	return openRegularUnix(path, unix.O_CREAT|unix.O_TRUNC|unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, uint32(mode.Perm()), "write")
}

func openRegularUnix(path string, flags int, mode uint32, operation string) (*os.File, error) {
	fd, err := unix.Open(path, flags, mode)
	if err != nil {
		// The bare errno names no file; every consumer's message —
		// the drift refusals above all — needs the path
		// (fs.PathError keeps errors.Is matching on the errno).
		return nil, &fs.PathError{Op: operation, Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("%s %s: not a regular file", operation, path)
	}
	return file, nil
}
