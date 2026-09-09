package gomutant

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// CampaignInputs is what a findings-producing run knows at its flag
// parse: the inputs every refusal below is decidable from, before the
// tree loads.
type CampaignInputs struct {
	// Selection is the run's declared build selection: the harness
	// environment it composes is judged here, before the lock.
	Selection Selection
	// FindingsPath is the findings document the campaign holds.
	FindingsPath string
	// ModuleDir is the tree root the store's layer judgment resolves
	// against.
	ModuleDir string
	// Plan is the preflight form: it takes no campaign lock, since it
	// persists nothing and must not mint the lock file a killed run
	// leaves behind.
	Plan bool
	// Budget and OracleTimeout are the run's bounds, sign-checked here.
	Budget        int
	OracleTimeout time.Duration
	// ScratchNamespaces, Vouches, and BracketPaths are the caller's
	// declarations, parsed here so a malformed one refuses before any
	// load; a bracket path is also proven present and capturable under
	// the tree root here, the base every spawn's bracket resolves
	// against.
	ScratchNamespaces []string
	Vouches           []string
	BracketPaths      []string
	// TargetSources names the target sources the caller supplied, in
	// the caller's own spelling — the flags or parameters given; at most
	// one may be given.
	TargetSources []string
}

// CampaignPreparation is the prepared campaign: every declaration
// parsed, the lock held, the exemptions and the store open — the
// material a run needs before its first load, produced by
// PrepareCampaign.
type CampaignPreparation struct {
	ScratchNamespaces []runtimeinput.ScratchNamespace
	Vouches           []string
	Exemptions        []Exemption
	Store             *Store
	// Prior is the findings document as read at preparation — the
	// document's own refusals (unreadable, a version this binary does
	// not read) fire here.
	Prior []Finding
	// ReleaseCampaign releases the campaign lock; a no-op for a plan.
	ReleaseCampaign func()
}

// validateRunBounds refuses a negative budget or oracle timeout — the
// one check the preparation and the library entry (Tree.Run) share.
func validateRunBounds(budget int, oracleTimeout time.Duration) error {
	if budget < 0 {
		return fmt.Errorf("gomutant: budget must be non-negative")
	}
	if oracleTimeout < 0 {
		return fmt.Errorf("gomutant: oracle timeout must be non-negative")
	}
	return nil
}

// ValidateTargetSources refuses more than one supplied target source,
// naming the ones given in the caller's own spelling.
func ValidateTargetSources(given []string) error {
	if len(given) > 1 {
		return fmt.Errorf("give one target source: %s were given - at most one", strings.Join(given, " and "))
	}
	return nil
}

// PrepareCampaign fires every refusal a findings-producing run can
// decide from its inputs alone, in one place and before any tree load:
// the bounds' signs, the target-source exclusivity, the scratch and
// vouch declarations, the selection's shape, the tree root's
// existence, the harness environment (the load ladder's
// input-decidable arm), the bracket paths' shape and presence, the
// exemptions document, the findings document (unreadable, or a version this
// binary does not read), and last the campaign lock (fail-fast — a
// second campaign against the same document refuses immediately,
// naming the holder, per REQ-exec-exclusivity) — last, because the
// lock's file outlives its holder by design, so no refusal may follow
// it. A run that fails any of them pays nothing (REQ-exec-preparation).
func PrepareCampaign(ctx context.Context, in CampaignInputs) (*CampaignPreparation, error) {
	if err := validateRunBounds(in.Budget, in.OracleTimeout); err != nil {
		return nil, err
	}
	if err := ValidateTargetSources(in.TargetSources); err != nil {
		return nil, err
	}
	scratch, err := ParseScratchNamespaces(in.ScratchNamespaces)
	if err != nil {
		return nil, err
	}
	var vouches []string
	if len(in.Vouches) > 0 {
		if vouches, err = ParseDynamicStateVouches(in.Vouches); err != nil {
			return nil, err
		}
	}
	if err := in.Selection.Validate(); err != nil {
		return nil, err
	}
	// The tree root is an input too: a root that is not a directory
	// refuses here, before the lock — whose directory creation would
	// otherwise conjure an empty tree for the load to find.
	info, err := os.Stat(in.ModuleDir)
	if err != nil {
		return nil, fmt.Errorf("gomutant: tree root %s: %w", in.ModuleDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("gomutant: tree root %s is not a directory", in.ModuleDir)
	}
	// The load ladder's environment arm is decidable from the root and
	// the selection alone: a GODEBUG that silences the harness's
	// build-fail events refuses here, before the lock, and again at the
	// load's head by construction (REQ-exec-provenance). The arm reads
	// the selection-applied environment the load reads — the term the
	// spec names, not a live dependence: no selection sets GODEBUG; the
	// selection's own shape refused above, with the declarations.
	if err := CheckHarnessEnvironment(in.ModuleDir, in.Selection); err != nil {
		return nil, err
	}
	// A bracket path's shape and presence are decidable from the tree
	// root alone (REQ-exec-observation) — refused here, before the lock
	// and the load; whether the bracket can fingerprint it is the run's
	// once-per-run capture at the loaded-set stage.
	if err := validateBracketPaths(in.ModuleDir, in.BracketPaths); err != nil {
		return nil, err
	}
	if err := bracketPathsExist(ctx, in.ModuleDir, in.BracketPaths); err != nil {
		return nil, err
	}
	// The store opens the exemptions record beside the document; the
	// document itself is read now, so its refusals fire before any load.
	store, err := OpenStore(in.FindingsPath, in.ModuleDir)
	if err != nil {
		return nil, err
	}
	prior, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	release := func() {}
	if !in.Plan {
		if release, err = AcquireCampaignLock(in.FindingsPath); err != nil {
			return nil, err
		}
	}
	return &CampaignPreparation{ScratchNamespaces: scratch, Vouches: vouches, Exemptions: store.Exemptions(), Store: store, Prior: prior, ReleaseCampaign: release}, nil
}

// ValidateEphemeralRuns refuses a runs count outside 1..MaxEphemeralRuns
// (0 reads as 1) — decidable at flag parse, so it fires before any load.
func ValidateEphemeralRuns(runs int) error {
	if runs < 0 || runs > MaxEphemeralRuns {
		return fmt.Errorf("runs %d is outside 1-%d (omitted means 1): each run is a full oracle process", runs, MaxEphemeralRuns)
	}
	return nil
}

// ValidateAttestationReason refuses an attestation whose reasoning is
// blank: the record carries the reasoning, so a probe that would end in
// an unrecordable attestation is refused before it runs.
func ValidateAttestationReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("gomutant: an equivalence attestation needs its reasoning on the record")
	}
	return nil
}

// ValidateRetargetPair refuses a malformed rename pair on the two
// strings alone — the predicates need no tree, so they fire before any
// load (REQ-result-lifecycle's pair shape).
func ValidateRetargetPair(from, to string) error {
	if from == "" || to == "" {
		return fmt.Errorf("retarget needs a non-empty from and to prefix")
	}
	if from == to {
		return fmt.Errorf("retarget needs distinct prefixes - from and to are both %q", from)
	}
	// The observation-subject halves rewrite under independent
	// projections of the pair; a structurally asymmetric pair (one half
	// package-shaped, the other symbol-shaped) would mangle the local
	// half into an identity that names nothing, durably.
	fromPkg, fromLocal := retargetPrefixParts(from)
	toPkg, toLocal := retargetPrefixParts(to)
	if (fromLocal == "") != (toLocal == "") {
		return fmt.Errorf("retarget prefixes must be structurally alike - %q and %q split package and symbol differently; use a package pair or a full-symbol pair", from, to)
	}
	// A symbol pair renames within its package: the destination carries
	// no stored fact to validate a package move against, and a dotted
	// destination remainder may continue a package instead of naming a
	// local - so the package halves must agree and the local halves map
	// segment for segment.
	if fromLocal != "" {
		if fromPkg != toPkg {
			return fmt.Errorf("retarget: a symbol pair renames within its package - %q and %q name different packages; move a surface across packages with a package pair", from, to)
		}
		if strings.Count(fromLocal, ".") != strings.Count(toLocal, ".") {
			return fmt.Errorf("retarget: %q -> %q restructures the local name - a rename maps segments one to one, and a dotted remainder may continue a package instead of naming a local; a package move takes a package pair, and a promotion or demotion that reshapes the name re-measures under the new shape", from, to)
		}
	}
	// Unlike-terminated pairs splice across unlike edges: with from
	// "example.com/old." and to "example.com/new", the matched dot is
	// consumed and never re-emitted, writing example.com/newTestF
	// durably. The terminator is part of the claim - both prefixes
	// carry the same one, or neither.
	terminator := func(p string) byte {
		if c := p[len(p)-1]; c == '.' || c == '/' {
			return c
		}
		return 0
	}
	if terminator(from) != terminator(to) {
		return fmt.Errorf("retarget prefixes must be like-terminated - %q and %q end differently, and splicing across unlike edges corrupts identities; terminate both with the same separator or neither", from, to)
	}
	return nil
}
