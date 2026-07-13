package contextio

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type cancelAfterChecks struct {
	context.Context
	remaining int
}

func (c *cancelAfterChecks) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestReadFilePrefersCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data, err := ReadFile(ctx, filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFile error = %v, want context.Canceled", err)
	}
	if data != nil {
		t.Fatalf("ReadFile data = %q, want nil", data)
	}
}

func TestWriteFilePrefersCancellationWithoutTruncating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WriteFile(ctx, path, []byte("replacement"), 0o600); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteFile error = %v, want context.Canceled", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("file contents = %q, want original contents", got)
	}
}

func TestFileRoundTripAcrossChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	want := bytes.Repeat([]byte("mutation-data"), chunkSize/4)
	if err := WriteFile(context.Background(), path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip returned %d bytes, want %d", len(got), len(want))
	}
}

func TestFileIOChecksCancellationBetweenChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	data := bytes.Repeat([]byte("x"), chunkSize*2)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFile(&cancelAfterChecks{Context: context.Background(), remaining: 2}, path); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("ReadFile = %d bytes, %v; want nil, context.Canceled", len(got), err)
	}
	if err := WriteFile(&cancelAfterChecks{Context: context.Background(), remaining: 2}, path, data, 0o600); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteFile error = %v, want context.Canceled", err)
	}
}
