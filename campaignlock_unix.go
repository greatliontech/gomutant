//go:build unix

package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AcquireCampaignLock takes the advisory campaign lock guarding the
// findings document at path and returns its release. A campaign holds
// it for its whole duration - measurement through final merge - so a
// second campaign against the same document refuses immediately,
// naming the holder, instead of interleaving measurements whose merges
// race (REQ-exec-exclusivity). Short document operations
// (dispositions, lifecycle verbs) serialize under the document lock
// alone and stay available while a campaign runs. It rides the shared
// flock core, fail-fast: one attempt, contention refuses at once.
func AcquireCampaignLock(path string) (release func(), err error) {
	lockPath := path + ".campaign"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	ensureLockIgnore(filepath.Dir(lockPath))
	release, holder, err := acquireFlock(context.Background(), lockPath, 1)
	if errors.Is(err, errFlockHeld) {
		if holder != "" {
			return nil, fmt.Errorf("gomutant: a campaign already holds %s (%s) - concurrent campaigns interleave measurements on one findings document; wait for it or stop it", path, holder)
		}
		return nil, fmt.Errorf("gomutant: a campaign already holds %s - concurrent campaigns interleave measurements on one findings document; wait for it or stop it", path)
	}
	if err != nil {
		return nil, fmt.Errorf("gomutant: campaign lock on %s: %w", path, err)
	}
	return release, nil
}
