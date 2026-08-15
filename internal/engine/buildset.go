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
	filePkg  map[string]string
}

func (t *Tree) buildIndex() *buildSet {
	t.build.once.Do(func() {
		t.build.packages = make(map[string]bool, len(t.pkgs))
		t.build.files = make(map[string]bool)
		t.build.filePkg = make(map[string]string)
		for _, pkg := range t.pkgs {
			t.build.packages[pkg.PkgPath] = true
			for _, file := range pkg.GoFiles {
				clean := filepath.Clean(file)
				t.build.files[clean] = true
				t.build.filePkg[clean] = pkg.PkgPath
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

// FileImportPath reports the loaded package import path compiling the
// absolute file, empty when no loaded package does - the coverage
// profile's file keys are import-path-qualified, so classifying a
// replacement against a profile starts here.
func (t *Tree) FileImportPath(abs string) string {
	return t.buildIndex().filePkg[filepath.Clean(abs)]
}
