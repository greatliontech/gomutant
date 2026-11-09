package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gotool"
)

// SchemaCache coalesces and caches compiled mutant-schema test binaries
// (`go test -c -overlay`) and shared `(dir, schemaKey)` overlays across
// candidates and oracle invocations within a single run (`OracleBounds`).
type SchemaCache struct {
	mu       sync.Mutex
	baseDir  string
	overlays map[string]*schemaOverlayEntry
	entries  map[string]*schemaCacheEntry
	compiled map[string]compiledSchemaBin
	pkgDirs  map[string]*pkgDirHygieneState
}

type pkgDirHygieneState struct {
	mu       sync.Mutex
	active   int
	baseline map[string]bool
}

type schemaOverlayEntry struct {
	once        sync.Once
	compileMu   sync.Mutex
	overlayPath string
	err         error
}

type schemaCacheEntry struct {
	once    sync.Once
	binPath string
	pkgDir  string
	err     error
}

type compiledSchemaBin struct {
	binPath string
	pkgDir  string
}

// NewSchemaCache creates an empty per-run schema binary cache.
func NewSchemaCache() *SchemaCache {
	return &SchemaCache{
		overlays: map[string]*schemaOverlayEntry{},
		entries:  map[string]*schemaCacheEntry{},
		compiled: map[string]compiledSchemaBin{},
		pkgDirs:  map[string]*pkgDirHygieneState{},
	}
}

func (c *SchemaCache) pkgDirState(cleanDir string) *pkgDirHygieneState {
	if c == nil {
		return &pkgDirHygieneState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pkgDirs == nil {
		c.pkgDirs = map[string]*pkgDirHygieneState{}
	}
	st, ok := c.pkgDirs[cleanDir]
	if !ok {
		st = &pkgDirHygieneState{}
		c.pkgDirs[cleanDir] = st
	}
	return st
}

// Close removes all compiled schema binaries and temporary overlay directories
// created by this cache.
func (c *SchemaCache) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	baseDir := c.baseDir
	c.baseDir = ""
	c.overlays = map[string]*schemaOverlayEntry{}
	c.entries = map[string]*schemaCacheEntry{}
	c.compiled = map[string]compiledSchemaBin{}
	c.mu.Unlock()
	if baseDir != "" {
		_ = os.RemoveAll(baseDir)
	}
}

func (c *SchemaCache) findCompiled(dir, testPkg string) (string, string, bool) {
	if c == nil || testPkg == "" {
		return "", "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.compiled[dir+"\x00"+testPkg]
	if !ok || b.binPath == "" {
		return "", "", false
	}
	return b.binPath, b.pkgDir, true
}

func (c *SchemaCache) getOrCompile(ctx context.Context, dir, packageDir, testPkg, schemaKey string, replacements []Replacement, env []string, bounds OracleBounds) (string, string, error) {
	overlayKey := dir + "\x00" + schemaKey
	key := overlayKey + "\x00" + testPkg
	c.mu.Lock()
	if c.baseDir == "" {
		base, err := os.MkdirTemp("", "gomutant-schemas-*")
		if err != nil {
			c.mu.Unlock()
			return "", "", err
		}
		c.baseDir = base
	}
	baseDir := c.baseDir
	if c.overlays == nil {
		c.overlays = map[string]*schemaOverlayEntry{}
	}
	ov, ok := c.overlays[overlayKey]
	if !ok {
		ov = &schemaOverlayEntry{}
		c.overlays[overlayKey] = ov
	}
	entry, ok := c.entries[key]
	if !ok {
		entry = &schemaCacheEntry{}
		c.entries[key] = entry
	}
	c.mu.Unlock()

	ov.once.Do(func() {
		ov.overlayPath, ov.err = writeSchemaOverlay(ctx, baseDir, replacements)
	})
	if ov.err != nil {
		if ctx.Err() != nil {
			c.mu.Lock()
			if c.overlays[overlayKey] == ov {
				delete(c.overlays, overlayKey)
			}
			if c.entries[key] == entry {
				delete(c.entries, key)
			}
			c.mu.Unlock()
		}
		return "", "", ov.err
	}

	entry.once.Do(func() {
		entry.binPath, entry.pkgDir, entry.err = compileSchemaEntry(ctx, baseDir, dir, packageDir, testPkg, ov.overlayPath, &ov.compileMu, env, bounds)
	})
	if entry.err != nil && ctx.Err() != nil {
		c.mu.Lock()
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	} else if entry.err == nil && entry.binPath != "" {
		c.mu.Lock()
		if c.compiled == nil {
			c.compiled = map[string]compiledSchemaBin{}
		}
		compKey := dir + "\x00" + testPkg
		if _, exists := c.compiled[compKey]; !exists {
			c.compiled[compKey] = compiledSchemaBin{binPath: entry.binPath, pkgDir: entry.pkgDir}
		}
		c.mu.Unlock()
	}
	return entry.binPath, entry.pkgDir, entry.err
}

func writeSchemaOverlay(ctx context.Context, baseDir string, replacements []Replacement) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	overlayDir, err := os.MkdirTemp(baseDir, "overlay-*")
	if err != nil {
		return "", err
	}
	replace := make(map[string]string, len(replacements))
	for i, r := range replacements {
		if r.File == "" || r.Source == nil {
			return "", fmt.Errorf("schema replacement %d is incomplete", i+1)
		}
		if _, dup := replace[r.File]; dup {
			return "", fmt.Errorf("schema replaces %s more than once", r.File)
		}
		mutFile := filepath.Join(overlayDir, fmt.Sprintf("replacement-%d%s", i, filepath.Ext(r.File)))
		if err := contextio.WriteFile(ctx, mutFile, r.Source, 0o644); err != nil {
			return "", err
		}
		replace[r.File] = mutFile
	}
	overlayPath := filepath.Join(overlayDir, "overlay.json")
	oj, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replace})
	if err != nil {
		return "", err
	}
	if err := contextio.WriteFile(ctx, overlayPath, oj, 0o644); err != nil {
		return "", err
	}
	return overlayPath, nil
}

func compileSchemaEntry(ctx context.Context, baseDir, dir, packageDir, testPkg, overlayPath string, compileMu *sync.Mutex, env []string, bounds OracleBounds) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	entryDir, err := os.MkdirTemp(baseDir, "pkg-*")
	if err != nil {
		return "", "", err
	}

	scratchEnv, _, sweepScratch, removeScratch, err := oracleScratch(env)
	if err != nil {
		return "", "", err
	}
	defer removeScratch()
	defer sweepScratch()

	pkgDir := packageDir
	if pkgDir == "" {
		cmd, err := oracleCommand(ctx, dir, oracleEnv(scratchEnv, bounds), "list", "-f", "{{.Dir}}", testPkg)
		if err != nil {
			return "", "", err
		}
		var listOut bytes.Buffer
		cmd.Stdout = &listOut
		if err := runOracleProcess(cmd, bounds); err != nil {
			return "", "", err
		}
		pkgDir = strings.TrimSpace(listOut.String())
		if pkgDir == "" {
			return "", "", fmt.Errorf("empty package directory for %s", testPkg)
		}
	}

	pkgBase := path.Base(strings.TrimSuffix(testPkg, "/"))
	if pkgBase == "" || pkgBase == "." || pkgBase == "/" {
		pkgBase = filepath.Base(pkgDir)
	}
	if pkgBase == "" || pkgBase == "." || pkgBase == string(filepath.Separator) {
		pkgBase = "pkg"
	}
	binName := pkgBase + ".test"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(entryDir, binName)
	if compileMu != nil {
		compileMu.Lock()
		defer compileMu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	cmd, err := oracleCommand(ctx, dir, oracleEnv(scratchEnv, bounds), "test", "-c", "-o", binPath, "-overlay", overlayPath, testPkg)
	if err != nil {
		return "", "", err
	}
	var compileOut bytes.Buffer
	cmd.Stdout = &compileOut
	cmd.Stderr = &compileOut
	if err := runOracleProcess(cmd, bounds); err != nil {
		return "", "", err
	}
	info, statErr := os.Stat(binPath)
	if statErr != nil || info.Size() == 0 {
		return "", "", fmt.Errorf("schema binary was not produced for %s", testPkg)
	}
	return binPath, pkgDir, nil
}

func snapshotDirEntries(dir string) map[string]bool {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(entries)+3)
	for _, e := range entries {
		out[e.Name()] = true
	}
	out[".git"] = true
	for _, p := range oracleBookkeepingPaths {
		out[p] = true
	}
	return out
}

func isScratchNamespaceEntry(pkgDir, name, treeRoot string, namespaces []gofresh.ScratchNamespace) bool {
	if len(namespaces) == 0 || pkgDir == "" {
		return false
	}
	cleanPkgDir := filepath.Clean(pkgDir)
	baseRoot := treeRoot
	if baseRoot == "" {
		baseRoot = cleanPkgDir
	}
	cleanRoot := filepath.Clean(baseRoot)
	for _, ns := range namespaces {
		nsDir := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(ns.Dir)))
		if nsDir == cleanPkgDir {
			if matched, err := filepath.Match(ns.Pattern, name); err == nil && matched {
				return true
			}
			continue
		}
		if rel, err := filepath.Rel(cleanPkgDir, nsDir); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			firstSeg, _, _ := strings.Cut(rel, string(filepath.Separator))
			if firstSeg == name {
				return true
			}
		}
	}
	return false
}

func sweepNewDirEntries(dir, treeRoot string, before map[string]bool, namespaces []gofresh.ScratchNamespace) {
	if dir == "" || before == nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if before[name] || isScratchNamespaceEntry(dir, name, treeRoot, namespaces) {
			continue
		}
		target := filepath.Join(dir, name)
		_ = filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
		_ = os.RemoveAll(target)
	}
}

func enterPkgDir(c *SchemaCache, pkgDir, treeRoot string, namespaces []gofresh.ScratchNamespace) func() {
	if pkgDir == "" {
		return func() {}
	}
	cleanDir := filepath.Clean(pkgDir)
	st := c.pkgDirState(cleanDir)
	st.mu.Lock()
	if st.active == 0 {
		st.baseline = snapshotDirEntries(cleanDir)
	}
	st.active++
	st.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			st.mu.Lock()
			defer st.mu.Unlock()
			sweepNewDirEntries(cleanDir, treeRoot, st.baseline, namespaces)
			st.active--
			if st.active <= 0 {
				st.active = 0
				st.baseline = nil
			}
		})
	}
}

func canUseSchemaBinary(m Mutant, testPkgs []string, baselineDir string, bounds OracleBounds) bool {
	if bounds.Schemas == nil {
		return false
	}
	if m.SchemaMutantID == "" || m.SchemaKey == "" || len(m.SchemaReplacements) == 0 {
		return false
	}
	if len(testPkgs) != 1 || testPkgs[0] == "" {
		return false
	}
	if baselineDir != "" {
		return false
	}
	return true
}

func runSchemaBinary(
	ctx context.Context,
	dir, binPath, pkgDir string,
	m Mutant,
	testPkg, runRegex string,
	timeout time.Duration,
	binFlags []string,
	moduleDir, packageDir string,
	bracketPaths []string,
	namespaces []gofresh.ScratchNamespace,
	env []string,
	sink *bytes.Buffer,
	memoryDecided *bool,
	bounds OracleBounds,
) (MutantOutcome, string, gofresh.Observation, string, string, error) {
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	capture := moduleDir != "" && packageDir != ""
	effectivePkgDir := pkgDir
	if packageDir != "" {
		effectivePkgDir = packageDir
	}

	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	defer os.RemoveAll(tmp)

	scratchEnv, scratchRoot, sweepScratch, removeScratch, err := oracleScratch(env)
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	defer removeScratch()

	parent := ctx
	runCtx, cancel := context.WithTimeoutCause(parent, timeout, errOracleBudgetExceeded)
	defer cancel()

	testlog := filepath.Join(tmp, "mutant.testlog")
	baseTestlog := filepath.Join(tmp, "baseline.testlog")

	binArgs := []string{
		"-test.paniconexit0",
		"-test.v=test2json",
		"-test.timeout=0",
		"-test.count=1",
		"-test.failfast=true",
		"-test.run=" + runRegex,
	}
	binArgs = append(binArgs, binFlags...)
	if capture {
		binArgs = append(binArgs, "-test.testlogfile="+testlog)
	}

	var oracleFrame gofresh.ProducerFrame
	if capture {
		oracleFrame = captureOracleFrame(ctx, dir, effectivePkgDir, bracketPaths)
	}

	execEnv := oracleEnv(gotool.SetEnv(scratchEnv, "PWD", effectivePkgDir), bounds)
	mutantExecEnv := gotool.SetEnv(execEnv, "GOMUTANT_ACTIVE_MUTANT", m.SchemaMutantID)
	mutantBinPath := binPath
	if m.SchemaMutantID != "" {
		mutantDir := filepath.Join(tmp, "_gomutant_active_"+m.SchemaMutantID)
		if err := os.MkdirAll(mutantDir, 0o700); err == nil {
			linked := filepath.Join(mutantDir, filepath.Base(binPath))
			if err := os.Link(binPath, linked); err == nil {
				mutantBinPath = linked
			}
		}
	}
	cmd, err := prepareSchemaBinaryCommand(runCtx, effectivePkgDir, mutantExecEnv, mutantBinPath, binArgs...)
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}

	stdout := &bytes.Buffer{}
	if sink != nil {
		stdout = sink
	}
	conv := newTest2JSONConverter(stdout, testPkg, test2jsonTimestamp)
	cmd.Stdout = conv
	cmd.Stderr = conv

	leavePkgDir := enterPkgDir(bounds.Schemas, effectivePkgDir, dir, namespaces)
	defer leavePkgDir()
	runErr := runOracleProcess(cmd, bounds)
	conv.Exited(runErr)
	_ = conv.Close()
	sweepScratch()
	leavePkgDir()

	if oracleBudgetFired(runErr, cmd.ProcessState, oracleProcessKilled(cmd), runCtx) {
		state, incomplete, err := processObservationContext(ctx, testlog, dir, "mutant test process timed out", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		return MutantKilled, TimeoutKiller, state, incomplete, "", err
	}
	if oracleRunCancelled(runErr, runCtx) {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, dir, "mutant test process was cancelled", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", gofresh.Observation{}, "", "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, "", ctx.Err()
	}

	killer, parseErr := firstFailingTest(stdout.Bytes())
	if parseErr != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, dir, "go test output was malformed before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", gofresh.Observation{}, "", "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, "", fmt.Errorf("parse go test output: %w", parseErr)
	}

	switch {
	case runErr == nil:
		state, incomplete, err := processObservationContext(ctx, testlog, dir, "", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		return MutantSurvived, "", state, incomplete, "", err
	case killer != "":
		reason := ""
		if testProcessPanicked(stdout.Bytes()) || !testFailureCompleted(stdout.Bytes(), killer) {
			reason = "mutant test process panicked before observation finalization"
			if !testProcessPanicked(stdout.Bytes()) {
				reason = "mutant test process exited before observation finalization"
			}
		}
		state, incomplete, err := processObservationContext(ctx, testlog, dir, reason, env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		markMemoryDecided(memoryDecided, stdout.Bytes(), nil)
		return MutantKilled, killer, state, incomplete, "", err
	}

	pkg := failedPackage(stdout.Bytes())
	if pkg == "" {
		pkg = testPkg
	}
	baseCtx, baseCancel := context.WithTimeout(parent, timeout)
	defer baseCancel()
	baseScratchEnv, baseScratchRoot, sweepBaseScratch, removeBaseScratch, err := oracleScratch(env)
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	defer removeBaseScratch()
	var baselineFrame gofresh.ProducerFrame
	if capture {
		baselineFrame = captureOracleFrame(ctx, dir, effectivePkgDir, bracketPaths)
	}
	baseBinArgs := []string{
		"-test.paniconexit0",
		"-test.v=test2json",
		"-test.timeout=0",
		"-test.count=1",
		"-test.failfast=true",
		"-test.run=" + runRegex,
	}
	baseBinArgs = append(baseBinArgs, binFlags...)
	if capture {
		baseBinArgs = append(baseBinArgs, "-test.testlogfile="+baseTestlog)
	}
	baseExecEnv := oracleEnv(gotool.SetEnv(baseScratchEnv, "PWD", effectivePkgDir), bounds)
	baseExecEnv = gotool.SetEnv(baseExecEnv, "GOMUTANT_ACTIVE_MUTANT", "")
	base, err := prepareSchemaBinaryCommand(baseCtx, effectivePkgDir, baseExecEnv, binPath, baseBinArgs...)
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	baseErr := runOracleProcess(base, bounds)
	sweepBaseScratch()

	mutantState, mutantIncomplete, err := processObservationContext(ctx, testlog, dir, "mutant test process exited before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
	if err != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", err
	}
	if baseCtx.Err() != nil {
		baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, dir, "baseline test process did not complete", env, baseScratchRoot, capture, baselineFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", gofresh.Observation{}, "", "", observationErr
		}
		state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, bounds, mutantState, baselineState)
		if mergeErr != nil {
			return MutantDiscarded, "", gofresh.Observation{}, "", "", mergeErr
		}
		if parent.Err() != nil {
			return MutantDiscarded, "", state, mutantIncomplete, "", parent.Err()
		}
		diagnostic := fmt.Sprintf("unclassifiable mutant-run failure: the baseline probe exceeded the oracle timeout (%s), so the failure is not provably mutant-caused: %v: %s", timeout, runErr, tail(stdout.String(), 400))
		markMemoryDecided(memoryDecided, stdout.Bytes(), nil)
		return MutantDiscarded, "", state, diagnostic, "", nil
	}
	if baseErr == nil {
		baselineState, _, err := processObservationContext(ctx, baseTestlog, dir, "", env, baseScratchRoot, capture, baselineFrame, namespaces, bounds)
		if err != nil {
			return MutantDiscarded, "", gofresh.Observation{}, "", "", err
		}
		state, err := mergeProcessObservationsContext(ctx, dir, env, capture, bounds, mutantState, baselineState)
		killer := PackageKillerPrefix + "unattributed crash)"
		if pkg != "" {
			killer = PackageKillerPrefix + pkg + ")"
		}
		markMemoryDecided(memoryDecided, stdout.Bytes(), nil)
		return MutantKilled, killer, state, mutantIncomplete, "", err
	}
	baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, dir, "baseline test process failed before observation finalization", env, baseScratchRoot, capture, baselineFrame, namespaces, bounds)
	if observationErr != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", observationErr
	}
	state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, bounds, mutantState, baselineState)
	if mergeErr != nil {
		return MutantDiscarded, "", gofresh.Observation{}, "", "", mergeErr
	}
	diagnostic := fmt.Sprintf("unclassifiable mutant-run failure: the baseline probe failed alongside the mutant (environmental noise, not a kill): %v: %s", runErr, tail(stdout.String(), 400))
	markMemoryDecided(memoryDecided, stdout.Bytes(), nil)
	return MutantDiscarded, "", state, diagnostic, "", nil
}
