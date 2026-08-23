package gomutant

import (
	"os"
	"path/filepath"
	"strings"
)

// ensureLockIgnore keeps the by-design persistent lock files out of
// consumers' commits: inside the tool-owned .gomutant directory a
// minted .gitignore covers *.campaign and *.lock, so an add-everything
// staging loop cannot commit a lock file whose persistence (the flock
// is the lock; the file deliberately outlives every holder) is
// invisible from its name. A findings document outside the tool-owned
// directory keeps its directory untouched - minting ignore rules in
// user-owned directories is not the tool's call; the documented lock
// lifecycle covers that placement. Best-effort: hygiene never blocks a
// campaign, and a concurrent double-append costs at most a duplicate
// ignore line.
func ensureLockIgnore(dir string) {
	if filepath.Base(dir) != ".gomutant" {
		return
	}
	path := filepath.Join(dir, ".gitignore")
	content, _ := os.ReadFile(path)
	var missing []string
	for _, pattern := range []string{"*.campaign", "*.lock"} {
		if !strings.Contains(string(content), pattern) {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(strings.Join(missing, "\n") + "\n")
}
