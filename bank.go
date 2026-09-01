package gomutant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The baseline bank (REQ-result-baseline-bank): baseline and
// coverage-probe measurements are CONTENT-PINNED evidence, banked
// machine-local and served across runs — a killed or finished
// campaign's measurement is never discarded by the calendar. Serving
// is sound by construction: the banked oracle-subject evidence rows
// re-verify against the current views through the same
// evidencePairsValid discipline finding serves use, and the banked
// observation re-enters ONLY through runtimeinput.AdoptEnv, which
// re-evaluates every recorded identity against disk and refuses on
// any disagreement, the adopted digest then compared against the
// banked one — a failed adoption or digest mismatch falls back to a
// fresh probe, never an error. Durations served at any age are safe for budget
// derivation because verdict integrity is evidence-gated elsewhere:
// a timeout kill needs a re-executable candidate-evidence row
// (REQ-exec-attribution's timeout clause), so a stale-fast banked
// baseline can cost re-runs, never a wrong verdict — which is why
// the campaign measurement leash plays no discard role here. The
// bank never enters the repo document: durations and probe
// identities are this machine's facts, keyed by the resolved tree
// exactly as the findings overlay is.

// bankVersion is the bank file's format version; a mismatched or
// unreadable file is discarded whole (a bank is pure cache — the
// re-measure IS the recovery path).
const bankVersion = 1

// bankFileCeiling bounds the bank read: keys embed oracle patterns,
// so membership churn orphans entries and the file only grows — a
// bank past the ceiling is discarded whole at open, exactly the
// overlay's stat-time posture (overlayEntryCeiling): orders of
// magnitude above healthy content, and eviction costs a re-measure.
const bankFileCeiling = 8 << 20

type baselineBankFile struct {
	Version   int                       `json:"version"`
	Baselines map[string]bankedBaseline `json:"baselines,omitempty"`
	Coverage  map[string]bankedCoverage `json:"coverage,omitempty"`
}

// bankedBaseline is one oracle group's banked passing-baseline
// measurement: the content pins (the group's oracle-subject evidence
// rows at measure time, plus the toolchain that measured), the
// observation's persisted manifest for adoption, and the raw
// wall-clock the budget derives from.
type bankedBaseline struct {
	// Evidence rows pin everything content-shaped — source closures,
	// guards (toolchain, build config), observation posture, recorded
	// runtime inputs — through the finding-serve pair discipline.
	Evidence  []SubjectEvidence `json:"evidence"`
	Manifest  string            `json:"manifest"`
	Digest    string            `json:"digest"`
	RawMillis int64             `json:"rawMillis"`
	// MeasuredAtUnix records when — diagnostic only; age never
	// discards a content-valid entry (see the package note).
	MeasuredAtUnix int64 `json:"measuredAtUnix"`
}

// bankedCoverage is one (group, cover package) coverage probe: the
// content pins (the group's oracle-subject rows AND the covered
// package's own row — coverage speaks about both sides), and the
// probed batches with their coverage and wall-clocks.
type bankedCoverage struct {
	Evidence []closureRow  `json:"evidence"`
	CoverRow closureRow    `json:"coverRow"`
	Batches  []bankedBatch `json:"batches"`
}

type bankedBatch struct {
	Fns       []string                 `json:"fns"`
	DurMillis int64                    `json:"durMillis"`
	Coverage  engine.PersistedCoverage `json:"coverage"`
}

// baselineBank is the in-memory bank for one run: loaded once, each
// deposit persisted atomically as it lands (save is the flush
// backstop). The campaign
// lock (REQ-exec-exclusivity) serializes findings-producing runs, so
// two writers never race the file.
type baselineBank struct {
	// mu guards the maps and the dirty flag: the preparation
	// goroutine consults while the window loop's commit deposits —
	// the same overlap pendingBankDeposits guards against. (The
	// campaign lock serializes cross-PROCESS writers of the file;
	// this mutex serializes this run's own goroutines.)
	mu    sync.Mutex
	path  string
	file  baselineBankFile
	dirty bool
}

// bankPath derives the bank's machine-local home from the resolved
// tree — the same keying the findings overlay uses, as a sibling.
func bankPath(moduleDir string) (string, error) {
	_, dir, err := machineLocalDir(moduleDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "baselines.json"), nil
}

// openBaselineBank loads the bank, treating every failure — absent
// file, unreadable, malformed, version-skewed — as an empty bank: a
// cache miss, never an error.
func openBaselineBank(moduleDir string) *baselineBank {
	b := &baselineBank{file: baselineBankFile{Version: bankVersion}}
	path, err := bankPath(moduleDir)
	if err != nil {
		return b
	}
	b.path = path
	if info, err := os.Stat(path); err != nil || info.Size() > bankFileCeiling {
		return b
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	var file baselineBankFile
	if json.Unmarshal(data, &file) != nil || file.Version != bankVersion {
		return b
	}
	b.file = file
	return b
}

// save flushes any unpersisted state — a backstop; deposits already
// persisted themselves.
func (b *baselineBank) save() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.persistLocked()
}

// persistLocked writes the bank atomically; a write failure is
// silent — the bank is cache, and the measurements it would have
// carried are re-measurable. Caller holds b.mu.
func (b *baselineBank) persistLocked() {
	if !b.dirty || b.path == "" {
		return
	}
	data, err := json.Marshal(b.file)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(b.path), 0o755) != nil {
		return
	}
	tmp := b.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if os.Rename(tmp, b.path) == nil {
		b.dirty = false
	}
}

func (b *baselineBank) baseline(key string) (bankedBaseline, bool) {
	if b == nil {
		return bankedBaseline{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.file.Baselines[key]
	return e, ok
}

func (b *baselineBank) putBaseline(key string, e bankedBaseline) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file.Baselines == nil {
		b.file.Baselines = map[string]bankedBaseline{}
	}
	b.file.Baselines[key] = e
	b.dirty = true
	// Deposits persist IMMEDIATELY: the bank exists to survive killed
	// campaigns, and an exit-time save dies with the process. Entries
	// are minutes apart; an atomic rewrite per deposit is noise
	// beside the probe it records.
	b.persistLocked()
}

func (b *baselineBank) coverage(key string) (bankedCoverage, bool) {
	if b == nil {
		return bankedCoverage{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.file.Coverage[key]
	return e, ok
}

func (b *baselineBank) putCoverage(key string, e bankedCoverage) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file.Coverage == nil {
		b.file.Coverage = map[string]bankedCoverage{}
	}
	b.file.Coverage[key] = e
	b.dirty = true
	b.persistLocked()
}

// pendingBankDeposit is a really-probed group baseline awaiting its
// pins: the probe knows the manifest and the wall-clock, but the
// observation-bearing evidence rows the pins need are built once, by
// the finding assembly (attaching an observation to a view is
// one-shot in gofresh, so the bank must not attach its own) — the
// deposit completes at commit time from the finding's own
// OracleEvidence rows, the exact rows finding freshness compares.
type pendingBankDeposit struct {
	manifest, digest string
	raw              time.Duration
}

// bankOracleRowsFor selects a finding's oracle evidence rows for one
// package — the group's pins at deposit time.
func bankOracleRowsFor(f Finding, pkg string) []SubjectEvidence {
	var rows []SubjectEvidence
	for _, e := range f.OracleEvidence {
		if p, _ := splitTestSymbol(e.Symbol); p == pkg {
			rows = append(rows, e)
		}
	}
	return rows
}

// closureRow is the coverage entries' lighter pin: source closures
// and guards alone. Coverage is a function of sources and toolchain —
// its runtime-dependent residue is exactly the risk class the
// narrowed-survivor audit already measures, within a run and across
// them alike — so a closure-and-guards match is the whole
// reuse condition.
type closureRow struct {
	Symbol             string `json:"symbol"`
	MaximalClosure     string `json:"maximalClosure"`
	TestVariantClosure string `json:"testVariantClosure"`
	Toolchain          string `json:"toolchain"`
	BuildConfig        string `json:"buildConfig"`
}

func closureRowOf(v *subjectView) closureRow {
	return closureRow{
		Symbol:             v.symbol,
		MaximalClosure:     v.fp.MaximalClosure,
		TestVariantClosure: v.fp.TestVariantClosure,
		Toolchain:          v.fp.Guards.Toolchain,
		BuildConfig:        v.fp.Guards.BuildConfig,
	}
}

// closureRows builds coverage pins for a set of views.
func closureRows(views []*subjectView) []closureRow {
	rows := make([]closureRow, 0, len(views))
	for _, v := range views {
		rows = append(rows, closureRowOf(v))
	}
	return rows
}

// closurePinsHold compares banked closure rows against the current
// views: same subjects, identical closures and guards.
func closurePinsHold(rows []closureRow, views []*subjectView) bool {
	if len(rows) != len(views) {
		return false
	}
	bySymbol := make(map[string]closureRow, len(rows))
	for _, r := range rows {
		if _, dup := bySymbol[r.Symbol]; dup {
			return false
		}
		bySymbol[r.Symbol] = r
	}
	for _, v := range views {
		if bySymbol[v.symbol] != closureRowOf(v) {
			return false
		}
	}
	return true
}

// bankPinsHold reports whether banked evidence rows still describe
// the current views: same subjects, and every row validates through
// evidencePairsValid — the SAME pair discipline finding serves use
// (source closures, guards, observation posture, and the recorded
// runtime inputs re-digested against disk through the memo).
func bankPinsHold(ctx context.Context, rows []SubjectEvidence, views []*subjectView) (bool, error) {
	if len(rows) != len(views) {
		return false, nil
	}
	bySymbol := make(map[string]SubjectEvidence, len(rows))
	for _, r := range rows {
		if _, dup := bySymbol[r.Symbol]; dup {
			return false, nil
		}
		bySymbol[r.Symbol] = r
	}
	pairs := make([]evidencePair, 0, len(views))
	for _, v := range views {
		r, ok := bySymbol[v.symbol]
		if !ok {
			return false, nil
		}
		pairs = append(pairs, evidencePair{subject: v, evidence: r, accept: acceptValidVerdict})
	}
	memo := newRuntimeMemo(runtimeinput.CurrentEnvContext)
	ok, err := evidencePairsValid(ctx, pairs, memo.once)
	if err != nil || !ok {
		return ok, err
	}
	return memo.verify(ctx)
}

// groupOracleViews selects the work's oracle views for one group's
// package — the subjects a banked group entry pins.
func groupOracleViews(w work, g group) []*subjectView {
	var views []*subjectView
	for _, v := range w.oracleViews {
		if v.subject.Package == g.pkgs[0] {
			views = append(views, v)
		}
	}
	return views
}

// baselineKey is the oracle group's memo identity — ONE spelling for
// the run's in-memory maps and the bank's persisted keys alike
// (Structural enforcement: two hand-synced spellings of one identity
// were collapsed here).
type baselineKey struct {
	pkg, run, flags, moduleDir, packageDir string
}

// baselineKeyFor is the one group→key projection.
func baselineKeyFor(g group) baselineKey {
	return baselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
}

// persistedKey renders the bank's entry key: the memo axes plus the
// run's observation-shaping scope (bracket paths, scratch
// namespaces). moduleDir and packageDir are absolute — a moved tree
// keys a different machine-local home entirely, so its entries
// simply re-measure.
func (k baselineKey) persistedKey(scope string) string {
	return k.pkg + "\x00" + k.run + "\x00" + k.flags + "\x00" + k.moduleDir + "\x00" + k.packageDir + "\x00" + scope
}

// bankScopeString folds the observation-shaping per-run declarations
// into one stable key component.
func bankScopeString(brackets []string, namespaces []runtimeinput.ScratchNamespace) string {
	bs := append([]string(nil), brackets...)
	sort.Strings(bs)
	var nss []string
	for _, ns := range namespaces {
		nss = append(nss, ns.Dir+":"+ns.Pattern)
	}
	sort.Strings(nss)
	// The class separator keeps a bracket path spelled "dir:pattern"
	// from aliasing a namespace declaration.
	return strings.Join(bs, "\x1f") + "\x1e" + strings.Join(nss, "\x1f")
}
