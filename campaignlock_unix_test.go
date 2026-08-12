//go:build unix

package gomutant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := AcquireCampaignLock(path); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("pid %d", os.Getpid())) {
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
