//go:build !unix

package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// acquireDocumentLock on non-unix hosts serializes findings-document
// updates through an O_EXCL lockfile carrying its holder's pid and
// start time. Without flock the kernel cannot release the lock with the
// process, so a crashed holder's residue needs the operator: the
// refusal names the recorded holder and the removal step. The supported
// platform carries the self-releasing discipline
// (REQ-exec-exclusivity).
func acquireDocumentLock(ctx context.Context, path string) (release func(), err error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid %d since %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if attempt >= 49 {
			holder, _ := os.ReadFile(lockPath)
			if h := strings.TrimSpace(string(holder)); h != "" {
				return nil, fmt.Errorf("gomutant: findings document %s locked by another session (%s); remove %s if its holder is gone", path, h, lockPath)
			}
			return nil, fmt.Errorf("gomutant: findings document %s locked by another session; remove %s if its holder is gone", path, lockPath)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
