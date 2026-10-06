package gomutant

import (
	"os"
	"path/filepath"
	"strings"
)

// ExitLogName is the MCP server's exit log, written under the served
// tree's machine-local state home (REQ-mcp-exit-log), and
// ExitLogRotatedName the one generation a rotation keeps.
const (
	ExitLogName        = "mcp.log"
	ExitLogRotatedName = ExitLogName + ".1"
)

// ExitLogPaths names the exit log and its kept generation under the
// tree's machine-local state home — $XDG_STATE_HOME/gomutant/repos/
// keyed by the resolved tree as the findings overlay is under the
// cache home — outside the served tree, whatever document a run
// serves; it refuses a tree root or a state home that does not resolve.
func ExitLogPaths(dir string) ([]string, error) {
	base, err := machineLocalStateDir(dir)
	if err != nil {
		return nil, err
	}
	return []string{filepath.Join(base, ExitLogName), filepath.Join(base, ExitLogRotatedName)}, nil
}

// StoreOwnPaths names the tool-owned directory's machine-local files
// under the tree root's default store that a process other than the
// run may write while it measures — the ignore a lock taker on the
// default document mints — the harness's own, never measurement
// residue; the run adds them to its own writes itself.
func StoreOwnPaths(dir string) []string {
	return []string{filepath.Join(filepath.Dir(FindingsPathAt(dir, "")), ".gitignore")}
}

// RunOwnWrites lists the tree paths a campaign writes for its own
// bookkeeping around a findings document — the document itself, its
// campaign and document locks, and (inside the tool-owned directory,
// the only place the tool mints one) the ignore file — for
// Options.OwnWrites: residue attribution names the measured code's
// tree writes, never the harness's own (the store's own paths the run
// adds on its own, from the tree root it knows — StoreOwnPaths).
func RunOwnWrites(findingsPath string) []string {
	attestations := EphemeralAttestationsPathFor(findingsPath)
	own := []string{
		findingsPath, findingsPath + ".campaign", findingsPath + ".lock",
		attestations, attestations + ".lock",
	}
	if dir := filepath.Dir(findingsPath); toolOwnedDir(dir) {
		own = append(own, filepath.Join(dir, ".gitignore"))
	}
	return own
}

// toolOwnedDir reports whether dir is the tool-owned .gomutant
// directory — the one place lock-hygiene files are minted.
func toolOwnedDir(dir string) bool {
	return filepath.Base(dir) == ".gomutant"
}

// storeIgnorePatterns are the tool-owned directory's machine-local
// files: the by-design persistent lock files — files an add-everything
// staging loop must never commit.
var storeIgnorePatterns = []string{"*.campaign", "*.lock"}

// EnsureStoreIgnore keeps the tool's machine-local files out of
// consumers' commits: inside the tool-owned .gomutant directory a
// minted .gitignore covers storeIgnorePatterns, so an add-everything
// staging loop cannot commit a lock file whose persistence (the flock
// is the lock; the file deliberately outlives every holder) is
// invisible from its name. Every writer of such a file mints it first
// — the two lock takers. A findings document
// outside the tool-owned directory keeps its directory untouched -
// minting ignore rules in user-owned directories is not the tool's
// call; the documented lock lifecycle covers that placement.
// Best-effort: hygiene never blocks a campaign, and a concurrent
// double-append costs at most a duplicate ignore line.
func EnsureStoreIgnore(dir string) {
	if !toolOwnedDir(dir) {
		return
	}
	path := filepath.Join(dir, ".gitignore")
	content, _ := os.ReadFile(path)
	var missing []string
	for _, pattern := range storeIgnorePatterns {
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
