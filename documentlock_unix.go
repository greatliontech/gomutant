//go:build unix

package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// acquireDocumentLock takes the exclusive per-document write lock that
// serializes findings-document updates (REQ-exec-exclusivity's short
// document operations). It rides the shared flock core: the kernel
// releases the lock with the process, so a crashed writer never
// leaves a stale block. Short operations want to wait their brief
// turn rather than refuse immediately, so contention retries on a
// 100ms cadence for a bounded budget before refusing with the live
// holder named — the holder is live by construction (the kernel
// would have granted a dead holder's lock), so the refusal never
// prescribes marker removal.
func acquireDocumentLock(ctx context.Context, path string) (release func(), err error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	ensureLockIgnore(filepath.Dir(lockPath))
	release, holder, err := acquireFlock(ctx, lockPath, 50)
	if errors.Is(err, errFlockHeld) {
		if holder != "" {
			return nil, fmt.Errorf("gomutant: findings document %s locked by another live session (%s); wait for it or stop it", path, holder)
		}
		return nil, fmt.Errorf("gomutant: findings document %s locked by another live session; wait for it or stop it", path)
	}
	if err != nil {
		return nil, fmt.Errorf("gomutant: document lock on %s: %w", path, err)
	}
	return release, nil
}
