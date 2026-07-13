//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package contextio

import (
	"fmt"
	"os"
)

func openRegularRead(path string) (*os.File, error) {
	file, err := os.Open(path)
	return validateRegular(file, err, path, "read")
}

func openRegularWrite(path string, mode os.FileMode) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	return validateRegular(file, err, path, "write")
}

func validateRegular(file *os.File, openErr error, path, operation string) (*os.File, error) {
	if openErr != nil {
		return nil, openErr
	}
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
