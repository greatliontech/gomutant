//go:build unix

package gomutant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The campaign lock excludes a second campaign fail-fast, naming the
// holder; release admits the next; and the lock rides the flock, not
// the file - a leftover file from a crashed holder never blocks
// (REQ-exec-exclusivity).
func TestCampaignLockExcludesSecondCampaign(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gomutant", "findings.json")
	release, err := AcquireCampaignLock(path)
	if err != nil {
		t.Fatal(err)
	}
	// Fail-fast is contract, not tuning (REQ-exec-exclusivity:
	// "refuses immediately"): the refusal must return without ever
	// entering the retry cadence — a single flock attempt is
	// microseconds; one 100ms retry wait would already breach this
	// bound.
	started := time.Now()
	_, err = AcquireCampaignLock(path)
	if elapsed := time.Since(started); elapsed > 80*time.Millisecond {
		t.Fatalf("second campaign refused after %v; fail-fast means no retry wait", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("pid %d", os.Getpid())) {
		t.Fatalf("second campaign = %v, want the refusal naming the holder", err)
	}
	release()
	release2, err := AcquireCampaignLock(path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	release2()

	// A leftover lock file with no live flock never blocks: the file is
	// not the lock.
	if err := os.WriteFile(path+".campaign", []byte("pid 999999 since long ago\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release3, err := AcquireCampaignLock(path)
	if err != nil {
		t.Fatalf("stale lock file blocked a campaign: %v", err)
	}
	release3()
}
