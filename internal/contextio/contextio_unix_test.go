//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package contextio

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(context.Background(), path); err == nil {
		t.Fatal("ReadFile accepted a FIFO")
	}
}

func TestWriteFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(context.Background(), path, []byte("data"), 0o600); err == nil {
		t.Fatal("WriteFile accepted a FIFO")
	}
}
