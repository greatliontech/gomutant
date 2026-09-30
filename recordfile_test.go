package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The one record-file writer keeps the file's own mode across a rewrite
// and gives a first write the default: a reviewed record a reviewer
// tightened stays tightened, a checkout-shared one stays readable
// (REQ-result-exemptions, REQ-result-record).
func TestWriteRecordFileKeepsTheFilesMode(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	fresh := filepath.Join(dir, "fresh.json")
	mode, err := recordFileMode(fresh)
	if err != nil || mode != 0o644 {
		t.Fatalf("absent file's mode = %v, %v; want the default 0644", mode, err)
	}
	if err := writeRecordFile(ctx, fresh, []byte("{}\n"), mode); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(fresh); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("first write's mode = %v, %v; want 0644", info.Mode(), err)
	}
	tight := filepath.Join(dir, "tight.json")
	if err := os.WriteFile(tight, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err = recordFileMode(tight)
	if err != nil || mode != 0o600 {
		t.Fatalf("tightened file's mode = %v, %v; want its own 0600", mode, err)
	}
	if err := writeRecordFile(ctx, tight, []byte("{\"v\":1}\n"), mode); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(tight); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rewrite's mode = %v, %v; want the file's own 0600 kept", info.Mode(), err)
	}
	if got, _ := os.ReadFile(tight); string(got) != "{\"v\":1}\n" {
		t.Fatalf("rewrite's contents = %q", got)
	}
	// A cancellation before the rename leaves the standing file and no
	// temporary behind.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := writeRecordFile(cancelled, tight, []byte("{\"v\":2}\n"), mode); err == nil {
		t.Fatal("a cancelled write landed")
	}
	if got, _ := os.ReadFile(tight); string(got) != "{\"v\":1}\n" {
		t.Fatalf("a cancelled write changed the file: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "fresh.json" && e.Name() != "tight.json" {
			t.Fatalf("a temporary survived the cancelled write: %s", e.Name())
		}
	}
}
