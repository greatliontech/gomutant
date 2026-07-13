//go:build !windows

package lib_test

import "syscall"

func countMarkerExistsAndSet(path string) (bool, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err == nil {
		_ = syscall.Close(fd)
		return true, nil
	}
	if err != syscall.ENOENT {
		return false, err
	}
	fd, err = syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	return false, syscall.Close(fd)
}
