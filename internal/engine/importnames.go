package engine

import "path/filepath"

// ImportedPackageName reports the declared package name of importPath
// as the loaded package owning the file at abs imports it — the name an
// unaliased import binds — false when that package does not import it
// (or the file belongs to no loaded package). The name comes from the
// loaded types, never from the import path's last element, which
// versioned and hyphenated paths contradict.
func (t *Tree) ImportedPackageName(abs, importPath string) (string, bool) {
	clean := filepath.Clean(abs)
	for _, pkg := range t.pkgs {
		if pkg.Types == nil {
			continue
		}
		owns := false
		for _, f := range pkg.GoFiles {
			if filepath.Clean(f) == clean {
				owns = true
				break
			}
		}
		if !owns {
			continue
		}
		for _, imported := range pkg.Types.Imports() {
			if imported.Path() == importPath {
				return imported.Name(), true
			}
		}
	}
	return "", false
}
