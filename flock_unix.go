//go:build unix

package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// errFlockHeld reports contention past the caller's retry budget; the
// caller shapes the refusal (the campaign lock refuses fail-fast, the
// document lock after a bounded wait), with the holder line read for
// display.
var errFlockHeld = errors.New("gomutant: lock held")

// acquireFlock opens (creating) lockPath and takes an exclusive
// flock(2) on it, retrying contention on a 100ms cadence up to
// attempts tries under ctx. The kernel releases the lock with the
// process, so a crashed holder never leaves a stale block, and the
// lockfile persists by design (the flock is the lock; removing it
// would let two waiters lock two inodes of the same name — the
// classic flock unlink race). On success the holder line (pid and
// start time — advisory display for refusals, never the lock itself)
// is truncated in and the release closure drops the lock; stale
// content is harmless because a successful acquire rewrites it and a
// refusal only ever reads content a live holder just wrote. On
// contention past the budget the current holder's line is returned
// with errFlockHeld.
func acquireFlock(ctx context.Context, lockPath string, attempts int) (release func(), holder string, err error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, "", err
	}
	for attempt := 1; ; attempt++ {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			// Only contention reads as a held lock; any other flock
			// failure (interrupt, an unsupported filesystem) surfaces
			// as itself. The caller's wrap carries the path.
			f.Close()
			return nil, "", fmt.Errorf("flock: %w", err)
		}
		if attempt >= attempts {
			content, _ := os.ReadFile(lockPath)
			f.Close()
			return nil, strings.TrimSpace(string(content)), errFlockHeld
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, "", ctx.Err()
		case <-timer.C:
		}
	}
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "pid %d since %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
		f.Sync()
	}
	return func() { f.Close() }, "", nil
}
