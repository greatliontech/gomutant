package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"golang.org/x/mod/modfile"
)

// errNoCoherentSignal reports that the tree's loader inputs cannot be
// fingerprinted from inside the tree, so no cached Tree is safe to serve.
var errNoCoherentSignal = errors.New("mcpserver: tree state has no coherent in-tree fingerprint")

// loadTreeContext returns a loaded Tree for the server's dir, reusing the
// previously loaded one only when a content fingerprint proves the loader
// would observe byte-identical inputs.
//
// What is safe to reuse: a loaded Tree is a pure function of the Go source
// the package loader reads — every .go file in the tree, the module and
// workspace files (go.mod, go.sum, go.work, go.work.sum, vendor/modules.txt),
// the toolchain the loader shells out to, the module cache, and the process
// environment. The user edits source between tool calls, so the cache key is
// a content hash over the in-tree file classes plus the toolchain's reported
// version; a hit means those bytes are identical and the reloaded Tree would
// be equal. The remaining inputs are stable by construction: the process
// environment is fixed for the server's lifetime, and the module cache is
// content-addressed and pinned by the hashed go.sum files. Two inputs escape
// the fingerprint and therefore disable caching entirely: a filesystem
// replace directive pointing outside the tree, and cgo (system headers feed
// type information from outside the tree). The key is recomputed on every
// call and a tree is cached only when the post-load fingerprint equals the
// pre-load one, so a hit proves the loader's inputs are byte-identical now —
// an edit racing a load, even one later reverted, is never served.
func (s *Server) loadTreeContext(ctx context.Context) (*gomutant.Tree, error) {
	key, err := treeStateKeyContext(ctx, s.dir)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Without a coherent fingerprint nothing is safely reusable: drop the
		// cache and load fresh rather than ever serving a stale tree.
		s.mu.Lock()
		s.tree, s.treeKey = nil, ""
		s.mu.Unlock()
		return gomutant.LoadContext(ctx, s.dir)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tree != nil && s.treeKey == key {
		return s.tree, nil
	}
	tree, err := gomutant.LoadContext(ctx, s.dir)
	if err != nil {
		s.tree, s.treeKey = nil, ""
		return nil, err
	}
	// The loaded tree is cached only when the fingerprint after the load
	// equals the one before it: a hit then proves the loader's inputs were
	// byte-identical before, during-modulo-restore, and now. Caching under the
	// pre-load key alone would let an edit racing the load — later reverted —
	// serve that edited tree forever against restored bytes.
	after, afterErr := treeStateKeyContext(ctx, s.dir)
	if afterErr != nil || after != key {
		s.tree, s.treeKey = nil, ""
		return tree, nil
	}
	s.tree, s.treeKey = tree, key
	return tree, nil
}

// treeStateKeyContext fingerprints every in-tree input class that can change
// what the package loader observes. Extra files (testdata, nested modules,
// underscore directories) are hashed too: over-invalidation only costs a
// reload, while a missed input would serve a stale tree.
func treeStateKeyContext(ctx context.Context, dir string) (string, error) {
	hash := sha256.New()
	// The go env output covers persistent GOENV-file state (go env -w GOFLAGS,
	// GOOS, GOEXPERIMENT, toolchain selection) that changes what the loader
	// observes without touching the tree or the process environment. Volatile
	// and dir-derived lines are dropped: GOGCCFLAGS embeds a fresh temporary
	// directory per invocation, and GOMOD/GOWORK restate paths whose contents
	// the walk below hashes directly.
	goEnv := exec.CommandContext(ctx, "go", "env")
	goEnv.Dir = dir
	envState, err := goEnv.Output()
	if err != nil {
		return "", err
	}
	for _, line := range bytes.Split(envState, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("GOGCCFLAGS=")) || bytes.HasPrefix(line, []byte("GOMOD=")) || bytes.HasPrefix(line, []byte("GOWORK=")) {
			continue
		}
		hash.Write(line)
		hash.Write([]byte{'\n'})
	}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cancelErr := ctx.Err(); cancelErr != nil {
			return cancelErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !loaderInputFile(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := contextio.ReadFile(ctx, path)
		if err != nil {
			return err
		}
		if err := rejectUnfingerprintableInput(dir, path, entry.Name(), data); err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d:%s%d:", len(rel), filepath.ToSlash(rel), len(data))
		hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func loaderInputFile(name string) bool {
	switch name {
	case "go.mod", "go.sum", "go.work", "go.work.sum", "modules.txt":
		return true
	}
	return strings.HasSuffix(name, ".go")
}

// rejectUnfingerprintableInput refuses caching when a hashed file pulls
// loader inputs from outside the tree: a filesystem replace directive whose
// target escapes the tree, or a cgo source file whose type information
// depends on system headers. The cgo probe over-detects (any quoted C import
// token) — the safe direction, since a false positive only disables caching.
func rejectUnfingerprintableInput(dir, path, name string, data []byte) error {
	if strings.HasSuffix(name, ".go") {
		if fileImportsC(path, data) {
			return errNoCoherentSignal
		}
		return nil
	}
	if name != "go.mod" && name != "go.work" {
		return nil
	}
	var replaces []*modfile.Replace
	if name == "go.mod" {
		file, err := modfile.Parse(path, data, nil)
		if err != nil {
			return errNoCoherentSignal
		}
		replaces = file.Replace
	} else {
		file, err := modfile.ParseWork(path, data, nil)
		if err != nil {
			return errNoCoherentSignal
		}
		replaces = file.Replace
	}
	for _, replace := range replaces {
		if replace.New.Version != "" {
			continue
		}
		target := replace.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		rel, err := filepath.Rel(dir, filepath.Clean(target))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errNoCoherentSignal
		}
	}
	return nil
}

// fileImportsC reports whether a Go source file imports "C" (cgo), parsed
// from its import list rather than probed as a raw byte pattern, so an
// ordinary string literal containing C never disables tree caching.
func fileImportsC(path string, data []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly)
	if err != nil {
		// Unparseable files fail the load anyway; refusing the cache is the
		// safe direction.
		return true
	}
	for _, spec := range file.Imports {
		if spec.Path != nil && spec.Path.Value == `"C"` {
			return true
		}
	}
	return false
}
