package gomutant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The baseline bank (REQ-result-baseline-bank): baseline
// measurements are CONTENT-PINNED evidence, banked
// machine-local and served across runs — a killed or finished
// campaign's measurement is never discarded by the calendar. Serving
// is sound by construction: the banked oracle-subject evidence rows
// re-verify against the current views through the same
// evidencePairsValid discipline finding serves use, and the banked
// observation re-enters ONLY through runtimeinput.Adopt, which
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
// unreadable file is discarded whole (a bank is pure cache — remeasurement
// is recovery). Version 3 records actual passing identities. Version 2 keeps
// its historical payloads, but cannot provide named-baseline authority.
const bankVersion = 3

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
	Evidence    []SubjectEvidence `json:"evidence"`
	Manifest    string            `json:"manifest"`
	Digest      string            `json:"digest"`
	RawMillis   int64             `json:"rawMillis"`
	PassedTests []string          `json:"passedTests"`
	// MeasuredAtUnix records when — diagnostic only; age never
	// discards a content-valid entry (see the package note).
	MeasuredAtUnix int64 `json:"measuredAtUnix"`
}

// bankedCoverage preserves historical coverage entries on decode and bank
// rewrites. Neither their pins nor their profiles authorize fresh scheduling;
// no production path restores, resumes or deposits coverage batches.
type bankedCoverage struct {
	Evidence []closureRow    `json:"evidence"`
	CoverRow closureRow      `json:"coverRow"`
	Plan     int             `json:"plan"`
	Batches  []bankedBatch   `json:"batches"`
	Failed   []bankedFailure `json:"failed,omitempty"`
}

type bankedBatch struct {
	Index     int                      `json:"index"`
	Fns       []string                 `json:"fns"`
	DurMillis int64                    `json:"durMillis"`
	Coverage  engine.PersistedCoverage `json:"coverage"`
}

// bankedFailure preserves a historical batch's failure attribution.
type bankedFailure struct {
	Index  int      `json:"index"`
	Fns    []string `json:"fns"`
	Reason string   `json:"reason"`
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
	// bank's file lock serializes cross-PROCESS writers of the file;
	// this mutex serializes this run's own goroutines.)
	mu        sync.Mutex
	path      string
	file      baselineBankFile
	dirty     bool
	dirtyKeys map[string]bool
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
	if json.Unmarshal(data, &file) != nil || (file.Version != bankVersion && file.Version != 2) {
		return b
	}
	if file.Version == 2 {
		file.Version = bankVersion
		for key, entry := range file.Baselines {
			entry.PassedTests = nil
			file.Baselines[key] = entry
		}
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

// persistLocked writes the bank atomically under an exclusive file lock,
// merging any concurrent writer's deposits from disk and bounding the
// serialized file below bankFileCeiling; a write failure is silent — the
// bank is cache, and the measurements it would have carried are
// re-measurable. Caller holds b.mu.
func (b *baselineBank) persistLocked() {
	if !b.dirty || b.path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(b.path), 0o755) != nil {
		return
	}
	release, lockErr := acquireDocumentLock(context.Background(), b.path)
	if lockErr == nil {
		defer release()
	}
	if info, err := os.Stat(b.path); err == nil && !info.IsDir() && info.Size() <= bankFileCeiling {
		if diskData, err := os.ReadFile(b.path); err == nil {
			var diskFile baselineBankFile
			if json.Unmarshal(diskData, &diskFile) == nil && (diskFile.Version == bankVersion || diskFile.Version == 2) {
				if diskFile.Version == 2 {
					for key, entry := range diskFile.Baselines {
						entry.PassedTests = nil
						diskFile.Baselines[key] = entry
					}
				}
				for key, entry := range diskFile.Baselines {
					if b.file.Baselines == nil {
						b.file.Baselines = map[string]bankedBaseline{}
					}
					cur, exists := b.file.Baselines[key]
					if !exists || (!b.dirtyKeys[key] && entry.MeasuredAtUnix > cur.MeasuredAtUnix) {
						b.file.Baselines[key] = entry
					}
				}
				for key, entry := range diskFile.Coverage {
					if b.file.Coverage == nil {
						b.file.Coverage = map[string]bankedCoverage{}
					}
					if _, exists := b.file.Coverage[key]; !exists {
						b.file.Coverage[key] = entry
					}
				}
			}
		}
	}
	data, err := json.Marshal(b.file)
	if err != nil {
		return
	}
	for len(data) > bankFileCeiling && len(b.file.Baselines) > 1 {
		oldestKey := ""
		var oldestTime int64
		first := true
		for k, e := range b.file.Baselines {
			if first || e.MeasuredAtUnix < oldestTime || (e.MeasuredAtUnix == oldestTime && k < oldestKey) {
				oldestKey = k
				oldestTime = e.MeasuredAtUnix
				first = false
			}
		}
		delete(b.file.Baselines, oldestKey)
		data, err = json.Marshal(b.file)
		if err != nil {
			return
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(b.path), ".baselines-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if os.Rename(tmpPath, b.path) == nil {
		b.dirty = false
		clear(b.dirtyKeys)
	} else {
		os.Remove(tmpPath)
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

// longestBaselineFor is the longest banked baseline of a test package
// under any run pattern, flags, or scope — the measurement leash's
// lift reads it (leashFor). A lift never tightens, so an
// over-inclusive match is safe: any measured duration of this
// package's tests is evidence that a bound below it would refuse an
// honest baseline.
func (b *baselineBank) longestBaselineFor(pkg string) (time.Duration, bool) {
	if b == nil {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var longest time.Duration
	found := false
	for key, e := range b.file.Baselines {
		if entryPkg, _, _ := strings.Cut(key, "\x00"); entryPkg != pkg {
			continue
		}
		if raw := time.Duration(e.RawMillis) * time.Millisecond; !found || raw > longest {
			longest, found = raw, true
		}
	}
	return longest, found
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
	if b.dirtyKeys == nil {
		b.dirtyKeys = map[string]bool{}
	}
	b.file.Baselines[key] = e
	b.dirtyKeys[key] = true
	b.dirty = true
	// Deposits persist IMMEDIATELY: the bank exists to survive killed
	// campaigns, and an exit-time save dies with the process. Entries
	// are minutes apart; an atomic rewrite per deposit is noise
	// beside the probe it records.
	b.persistLocked()
}

// depositGroupBaseline banks a passing, verifiable group baseline
// measurement at probe time — before any mutant executes — so a
// campaign killed during its first window still preserves the baseline
// measurement on disk (REQ-result-baseline-bank). It constructs a
// gofresh.Snapshot.Sibling over the group's oracle subjects, attaches the
// baseline's own observation, validates the producer bracket, and
// persists immediately. Any non-cancellation attachment or validation
// failure skips the deposit without aborting the campaign.
func depositGroupBaseline(ctx context.Context, bank *baselineBank, key string, w work, g group, observed runtimeinput.Observation, runEnv []string, raw time.Duration, passed []string) (bool, error) {
	if bank == nil || observed.Unverifiable || observed.Manifest == "" || w.producer == nil {
		return false, nil
	}
	views := groupOracleViews(w, g)
	if len(views) == 0 {
		return false, nil
	}
	type modSubjects struct {
		mod      *moduleSubjectView
		subjects []gofresh.Subject
		views    []*subjectView
	}
	var order []*modSubjects
	byMod := map[*moduleSubjectView]*modSubjects{}
	for _, v := range views {
		if v == nil || v.module == nil || v.module.view == nil {
			return false, nil
		}
		ms := byMod[v.module]
		if ms == nil {
			ms = &modSubjects{mod: v.module}
			byMod[v.module] = ms
			order = append(order, ms)
		}
		ms.subjects = append(ms.subjects, v.subject)
		ms.views = append(ms.views, v)
	}
	siblingSet := &subjectViewSet{
		bySymbol:       make(map[string]*subjectView, len(views)),
		bounds:         w.producer.bounds,
		packageProcess: w.producer.packageProcess,
	}
	for _, ms := range order {
		sView, err := ms.mod.view.Sibling(ms.subjects)
		if err != nil {
			return false, nil
		}
		siblingMod := &moduleSubjectView{
			view:     sView,
			validate: sView.Validate,
			producer: true,
		}
		siblingSet.modules = append(siblingSet.modules, siblingMod)
		for _, v := range ms.views {
			sv := &subjectView{
				symbol:      v.symbol,
				subject:     v.subject,
				moduleDir:   v.moduleDir,
				evidenceDir: v.evidenceDir,
				env:         v.env,
				view:        sView,
				fp:          v.fp,
				sourceFiles: v.sourceFiles,
				module:      siblingMod,
			}
			siblingSet.bySymbol[v.symbol] = sv
		}
	}
	siblingViews := make([]*subjectView, 0, len(views))
	for _, v := range views {
		siblingViews = append(siblingViews, siblingSet.bySymbol[v.symbol])
	}
	portable := newPortableUnion(observed, runEnv)
	rows, err := attachOracleEvidence(siblingViews, portable)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	unavailable, err := producerValidation(ctx, siblingSet)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if unavailable != "" {
		return false, nil
	}
	for _, r := range rows {
		if r.RuntimeUnverifiable {
			return false, nil
		}
	}
	bank.putBaseline(key, bankedBaseline{
		Evidence:       rows,
		Manifest:       observed.Manifest,
		Digest:         observed.Digest,
		PassedTests:    slices.Clone(passed),
		RawMillis:      raw.Milliseconds(),
		MeasuredAtUnix: time.Now().Unix(),
	})
	return true, nil
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
	passed           []string
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

// closureRow is the source-and-guard inventory stored in historical coverage
// entries. It is decoded as data, never judged as omission authority.
type closureRow struct {
	Symbol             string `json:"symbol"`
	MaximalClosure     string `json:"maximalClosure"`
	TestVariantClosure string `json:"testVariantClosure"`
	Toolchain          string `json:"toolchain"`
	BuildConfig        string `json:"buildConfig"`
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
	memo := newCurrentRuntimeMemo()
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
