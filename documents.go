package gomutant

import "path/filepath"

// FindingsPathAt resolves a findings document the caller named — empty
// for the default document — under the tree root: an absolute path
// stands, a relative one is tree-relative. Both faces resolve through
// this one rule (REQ-mcp-findings-doc).
func FindingsPathAt(dir, path string) string {
	if path == "" {
		path = DefaultFindingsPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, filepath.FromSlash(path))
}
