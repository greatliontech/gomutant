//go:build unix

package gomutant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// AcquireCampaignLock takes the advisory campaign lock guarding the
// findings document at path and returns its release. A campaign holds
// it for its whole duration - measurement through final merge - so a
// second campaign against the same document refuses immediately,
// naming the holder, instead of interleaving measurements whose merges
// race (REQ-exec-exclusivity). Short document operations
// (dispositions, lifecycle verbs) serialize under the document lock
// alone and stay available while a campaign runs. The lock is a
// flock(2) on a sibling file: the kernel releases it with the process,
// so a crashed campaign never leaves a stale lock; the pid and start
// time written inside are advisory display for the refusal.
func AcquireCampaignLock(path string) (release func(), err error) {
	lockPath := path + ".campaign"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := os.ReadFile(lockPath)
		f.Close()
		if err != syscall.EWOULDBLOCK {
			// Only contention reads as a running campaign; any other
			// flock failure (interrupt, an unsupported filesystem)
			// surfaces as itself.
			return nil, fmt.Errorf("gomutant: campaign lock on %s: %w", path, err)
		}
		if h := strings.TrimSpace(string(holder)); h != "" {
			return nil, fmt.Errorf("gomutant: a campaign already holds %s (%s) - concurrent campaigns interleave measurements on one findings document; wait for it or stop it", path, h)
		}
		return nil, fmt.Errorf("gomutant: a campaign already holds %s - concurrent campaigns interleave measurements on one findings document; wait for it or stop it", path)
	}
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "pid %d since %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
		f.Sync()
	}
	return func() {
		// The file persists; the flock is the lock. Removing it here
		// would let two waiters lock two inodes of the same name - the
		// classic flock unlink race - and stale content is harmless: a
		// successful TryLock truncates and rewrites it, and a refusal
		// only ever reads content a live holder just wrote.
		f.Close()
	}, nil
}
