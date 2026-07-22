package engine

import (
	"path/filepath"
	"sync"
)

// buildSet indexes the loaded build once: package import paths and the
// absolute Go files the build compiles, so ephemeral validation can
// refuse inputs the build would silently ignore
// (REQ-exec-ephemeral's validation arm).
type buildSet struct {
	once     sync.Once
	packages map[string]bool
	files    map[string]bool
}

func (t *Tree) buildIndex() *buildSet {
	t.build.once.Do(func() {
		t.build.packages = make(map[string]bool, len(t.pkgs))
		t.build.files = make(map[string]bool)
		for _, pkg := range t.pkgs {
			t.build.packages[pkg.PkgPath] = true
			for _, file := range pkg.GoFiles {
				t.build.files[filepath.Clean(file)] = true
			}
		}
	})
	return &t.build
}

// HasPackage reports whether path names a loaded package import path.
func (t *Tree) HasPackage(path string) bool {
	return t.buildIndex().packages[path]
}

// BuildCompilesFile reports whether the loaded build compiles the
// absolute file: a build-constraint-excluded source or a data file is
// not in any loaded package's GoFiles, so an overlay of it can never be
// exercised.
func (t *Tree) BuildCompilesFile(abs string) bool {
	return t.buildIndex().files[filepath.Clean(abs)]
}
