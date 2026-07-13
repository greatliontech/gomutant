//go:build windows

package lib_test

import "syscall"

func countMarkerExistsAndSet(path string) (bool, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err == nil {
		_ = syscall.CloseHandle(handle)
		return true, nil
	}
	if err != syscall.ERROR_FILE_NOT_FOUND && err != syscall.ERROR_PATH_NOT_FOUND {
		return false, err
	}
	handle, err = syscall.CreateFile(name, syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false, err
	}
	return false, syscall.CloseHandle(handle)
}
