package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/contextio"
)

// SchemaCache coalesces and caches compiled mutant-schema test binaries
// (`go test -c -overlay`) across candidates and oracle invocations within a
// single run (`OracleBounds`).
type SchemaCache struct {
	mu      sync.Mutex
	baseDir string
	entries map[string]*schemaCacheEntry
}

type schemaCacheEntry struct {
	once    sync.Once
	binPath string
	pkgDir  string
	err     error
}

// NewSchemaCache creates an empty per-run schema binary cache.
func NewSchemaCache() *SchemaCache {
	return &SchemaCache{
		entries: map[string]*schemaCacheEntry{},
	}
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
	c.entries = map[string]*schemaCacheEntry{}
	c.mu.Unlock()
	if baseDir != "" {
		_ = os.RemoveAll(baseDir)
	}
}

func (c *SchemaCache) getOrCompile(ctx context.Context, dir, packageDir, testPkg, schemaKey string, replacements []Replacement, env []string, bounds OracleBounds) (string, string, error) {
	key := dir + "\x00" + schemaKey + "\x00" + testPkg
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
	entry, ok := c.entries[key]
	if !ok {
		entry = &schemaCacheEntry{}
		c.entries[key] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		entry.binPath, entry.pkgDir, entry.err = compileSchemaEntry(ctx, baseDir, dir, packageDir, testPkg, replacements, env, bounds)
	})
	if entry.err != nil && ctx.Err() != nil {
		c.mu.Lock()
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return entry.binPath, entry.pkgDir, entry.err
}

func compileSchemaEntry(ctx context.Context, baseDir, dir, packageDir, testPkg string, replacements []Replacement, env []string, bounds OracleBounds) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	entryDir, err := os.MkdirTemp(baseDir, "pkg-*")
	if err != nil {
		return "", "", err
	}
	replace := make(map[string]string, len(replacements))
	for i, r := range replacements {
		if r.File == "" || r.Source == nil {
			return "", "", fmt.Errorf("schema replacement %d is incomplete", i+1)
		}
		if _, dup := replace[r.File]; dup {
			return "", "", fmt.Errorf("schema replaces %s more than once", r.File)
		}
		mutFile := filepath.Join(entryDir, fmt.Sprintf("replacement-%d%s", i, filepath.Ext(r.File)))
		if err := contextio.WriteFile(ctx, mutFile, r.Source, 0o644); err != nil {
			return "", "", err
		}
		replace[r.File] = mutFile
	}
	overlayPath := filepath.Join(entryDir, "overlay.json")
	oj, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replace})
	if err != nil {
		return "", "", err
	}
	if err := contextio.WriteFile(ctx, overlayPath, oj, 0o644); err != nil {
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

	binName := "pkg.test"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(entryDir, binName)
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
	namespaces []runtimeinput.ScratchNamespace,
	env []string,
	sink *bytes.Buffer,
	memoryDecided *bool,
	bounds OracleBounds,
) (MutantOutcome, string, runtimeinput.Observation, string, string, error) {
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	capture := moduleDir != "" && packageDir != ""
	effectivePkgDir := pkgDir
	if packageDir != "" {
		effectivePkgDir = packageDir
	}

	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	defer os.RemoveAll(tmp)

	scratchEnv, scratchRoot, sweepScratch, removeScratch, err := oracleScratch(env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
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

	var oracleFrame runtimeinput.ProducerFrame
	if capture {
		oracleFrame = captureOracleFrame(ctx, dir, effectivePkgDir, bracketPaths)
	}

	execEnv := oracleEnv(gotool.SetEnv(scratchEnv, "PWD", effectivePkgDir), bounds)
	mutantExecEnv := gotool.SetEnv(execEnv, "GOMUTANT_ACTIVE_MUTANT", m.SchemaMutantID)
	cmd, err := prepareSchemaBinaryCommand(runCtx, effectivePkgDir, mutantExecEnv, binPath, binArgs...)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}

	stdout := &bytes.Buffer{}
	if sink != nil {
		stdout = sink
	}
	conv := newTest2JSONConverter(stdout, testPkg, test2jsonTimestamp)
	cmd.Stdout = conv
	cmd.Stderr = conv

	runErr := runOracleProcess(cmd, bounds)
	conv.Exited(runErr)
	_ = conv.Close()
	sweepScratch()

	if oracleBudgetFired(runErr, cmd.ProcessState, oracleProcessKilled(cmd), runCtx) {
		state, incomplete, err := processObservationContext(ctx, testlog, dir, "mutant test process timed out", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		return MutantKilled, TimeoutKiller, state, incomplete, "", err
	}
	if oracleRunCancelled(runErr, runCtx) {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, dir, "mutant test process was cancelled", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, "", ctx.Err()
	}

	killer, parseErr := firstFailingTest(stdout.Bytes())
	if parseErr != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, dir, "go test output was malformed before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
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
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	defer removeBaseScratch()
	var baselineFrame runtimeinput.ProducerFrame
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
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	baseErr := runOracleProcess(base, bounds)
	sweepBaseScratch()

	mutantState, mutantIncomplete, err := processObservationContext(ctx, testlog, dir, "mutant test process exited before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces, bounds)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	if baseCtx.Err() != nil {
		baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, dir, "baseline test process did not complete", env, baseScratchRoot, capture, baselineFrame, namespaces, bounds)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
		}
		state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, bounds, mutantState, baselineState)
		if mergeErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", mergeErr
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
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
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
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
	}
	state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, bounds, mutantState, baselineState)
	if mergeErr != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", mergeErr
	}
	diagnostic := fmt.Sprintf("unclassifiable mutant-run failure: the baseline probe failed alongside the mutant (environmental noise, not a kill): %v: %s", runErr, tail(stdout.String(), 400))
	markMemoryDecided(memoryDecided, stdout.Bytes(), nil)
	return MutantDiscarded, "", state, diagnostic, "", nil
}
