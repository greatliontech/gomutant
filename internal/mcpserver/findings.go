package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type discoverIn struct {
	selectionIn
	TargetsPath string   `json:"targets_path,omitempty"`
	TargetsJSON string   `json:"targets_json,omitempty"`
	Changed     string   `json:"changed,omitempty"`
	Packages    []string `json:"packages,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
	Detail      bool     `json:"detail,omitempty"`
}

type discoverTarget struct {
	Symbol         string   `json:"symbol" jsonschema:"fully qualified target symbol"`
	OracleSet      int      `json:"oracleSet" jsonschema:"zero-based id that references one entry in the top-level oracleSets array"`
	Labels         []string `json:"labels,omitempty" jsonschema:"sorted opaque labels carried unchanged from the target"`
	OracleExplicit bool     `json:"oracleExplicit,omitempty" jsonschema:"whether the referenced oracle was explicitly supplied rather than package-derived"`
	Skipped        string   `json:"skipped,omitempty" jsonschema:"reason this target cannot be measured, when applicable"`
}

type discoverOracleSet struct {
	ID     int      `json:"id" jsonschema:"zero-based oracle-set id referenced by targets[].oracleSet"`
	Oracle []string `json:"oracle" jsonschema:"sorted fully qualified test symbols forming this exact effective oracle"`
}

type discoverOut struct {
	TargetCount       int                 `json:"targetCount" jsonschema:"effective targets after filtering; leads the response so a campaign's scale reads before any row"`
	SkippedCount      int                 `json:"skippedCount,omitempty" jsonschema:"targets carrying a skip reason"`
	ResidueCount      int                 `json:"residueCount,omitempty" jsonschema:"changed-but-untargeted paths"`
	OracleSets        []discoverOracleSet `json:"oracleSets" jsonschema:"canonical exact oracle sets assigned in first-target order; capped beside the target rows - sets beyond the cap are referenced only by omitted targets"`
	Targets           []discoverTarget    `json:"targets" jsonschema:"ordered effective targets whose oracleSet references oracleSets[].id; capped at 50 unless detail=true"`
	OmittedTargets    int                 `json:"omittedTargets,omitempty" jsonschema:"target rows beyond the cap; set detail=true for the full set"`
	OmittedOracleSets int                 `json:"omittedOracleSets,omitempty" jsonschema:"oracle sets beyond the cap; set detail=true for the full set"`
	Residue           []gomutant.Residue  `json:"residue,omitempty"`
	OmittedResidue    int                 `json:"omittedResidue,omitempty"`
	Note              string              `json:"note,omitempty" jsonschema:"set when discovery selected zero targets: names the input that emptied the selection and the next step"`
}

func (s *Server) toolDiscover(ctx context.Context, req *mcp.CallToolRequest, in discoverIn) (*mcp.CallToolResult, discoverOut, error) {
	var out discoverOut
	request, err := gomutant.PrepareSelection(ctx, s.dir, wireTargetSources(in.TargetsPath, in.TargetsJSON, in.Changed), s.targetInputs(in.TargetsPath, in.TargetsJSON, in.Changed), in.selection(), in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	tree, err := s.loadTreeReporting(ctx, notify, in.selection())
	if err != nil {
		return nil, out, err
	}
	// The selection and its descriptions ride the heartbeat under the
	// selection's stretch, as the CLI's cadence names them: resolving
	// every target's oracle is the expensive half (REQ-exec-run-status).
	type selected struct {
		sel          gomutant.TargetSelection
		descriptions []gomutant.TargetDescription
	}
	label := newStretchLabel(gomutant.StretchSelecting)
	described, err := withHeartbeatLabel(ctx, notify, label.get, func(ctx context.Context) (selected, error) {
		sel, err := s.selectTargets(ctx, tree, request)
		if err != nil {
			return selected{}, err
		}
		descriptions, err := tree.DescribeTargets(ctx, sel.Targets)
		return selected{sel: sel, descriptions: descriptions}, err
	})
	if err != nil {
		return nil, out, err
	}
	out.Residue = described.sel.Residue
	out.OracleSets, out.Targets = compactTargetDescriptions(described.descriptions)
	out.TargetCount = len(out.Targets)
	for _, target := range out.Targets {
		if target.Skipped != "" {
			out.SkippedCount++
		}
	}
	out.ResidueCount = len(out.Residue)
	// An empty selection is an answer, not silence: the note names the
	// input that emptied it, one discrimination shared with run
	// (REQ-mcp-envelope).
	if out.TargetCount == 0 {
		out.Note = gomutant.SelectionEmptiedNote(in.TargetsPath != "" || in.TargetsJSON != "", in.Changed, "changed")
	}
	out.capUnlessDetail(in.Detail)
	return nil, out, nil
}

// capUnlessDetail bounds the discovery row lists at the envelope cap
// with counted remainders unless the caller opted into the full set
// (REQ-mcp-envelope). Oracle sets are assigned in first-target order,
// so every retained target's set id stays within the same cap that
// bounds the target rows.
func (out *discoverOut) capUnlessDetail(detail bool) {
	if detail {
		return
	}
	out.Targets, out.OmittedTargets = capRows(out.Targets)
	out.OracleSets, out.OmittedOracleSets = capRows(out.OracleSets)
	out.Residue, out.OmittedResidue = capRows(out.Residue)
}

func compactTargetDescriptions(descriptions []gomutant.TargetDescription) ([]discoverOracleSet, []discoverTarget) {
	sets := make([]discoverOracleSet, 0)
	setByKey := map[string]int{}
	targets := make([]discoverTarget, 0, len(descriptions))
	for _, description := range descriptions {
		var key strings.Builder
		for _, oracle := range description.Oracle {
			fmt.Fprintf(&key, "%d:", len(oracle))
			key.WriteString(oracle)
		}
		id, ok := setByKey[key.String()]
		if !ok {
			id = len(sets)
			setByKey[key.String()] = id
			sets = append(sets, discoverOracleSet{ID: id, Oracle: description.Oracle})
		}
		targets = append(targets, discoverTarget{
			Symbol: description.Symbol, OracleSet: id, Labels: description.Labels,
			OracleExplicit: description.OracleExplicit, Skipped: description.Skipped,
		})
	}
	return sets, targets
}

type findingsIn struct {
	selectionIn
	Label    string `json:"label,omitempty"`
	State    string `json:"state,omitempty"`
	Judge    bool   `json:"judge,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Changed  string `json:"changed,omitempty"`
	Run      string `json:"run,omitempty"`
	Detail   bool   `json:"detail,omitempty"`
	Findings string `json:"findings,omitempty"`
}

// findingSummary is the bounded default row: enough to triage - what
// record, what state, which layer, how much is open - with the full
// lists behind detail (REQ-mcp-envelope, REQ-result-inspection).
type findingSummary struct {
	oracleExecutionPolicyOut
	Symbol    string                `json:"symbol"`
	Selection string                `json:"selection,omitempty"`
	State     gomutant.FindingState `json:"state"`
	Reason    string                `json:"reason,omitempty"`
	Layer     string                `json:"layer"`
	Run       string                `json:"run,omitempty" jsonschema:"identity of the run that last measured the record; absent on records measured before runs carried one"`
	Open      int                   `json:"open"`
	DeltaOpen *int                  `json:"deltaOpen,omitempty" jsonschema:"under changed: the open survivors on lines added since the ref"`
	Attested  int                   `json:"attested"`
	Withdrawn int                   `json:"withdrawn,omitempty"`
}

type inspectedFinding struct {
	oracleExecutionPolicyOut
	Symbol         string                          `json:"symbol"`
	Selection      string                          `json:"selection,omitempty"`
	Labels         []string                        `json:"labels,omitempty"`
	State          gomutant.FindingState           `json:"state"`
	Reason         string                          `json:"reason,omitempty"`
	Layer          string                          `json:"layer" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay"`
	LayerReason    string                          `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
	Run            string                          `json:"run,omitempty" jsonschema:"identity of the run that last measured the record"`
	DeltaOpen      []gomutant.Survivor             `json:"deltaOpen,omitempty" jsonschema:"under changed: the open survivors on lines added since the ref, listed distinctly"`
	CandidateCount int                             `json:"candidateCount"`
	Generated      int                             `json:"generated"`
	Mutants        int                             `json:"mutants"`
	Killed         int                             `json:"killed"`
	Discarded      int                             `json:"discarded"`
	Operators      []gomutant.OperatorSummary      `json:"operators"`
	Open           []gomutant.Survivor             `json:"open"`
	Attested       []gomutant.Attestation          `json:"attested"`
	Withdrawn      []gomutant.WithdrawnAttestation `json:"withdrawn,omitempty"`
	Candidates     []gomutant.CandidateEvidence    `json:"candidateEvidence,omitempty"`
}

// coverageBoundOut is one served coverage bound: the document's row
// with its roster capped and the remainder counted (REQ-mcp-envelope).
type coverageBoundOut struct {
	Selection        string   `json:"selection"`
	Run              string   `json:"run,omitempty"`
	Unreached        []string `json:"unreached"`
	OmittedUnreached int      `json:"omittedUnreached,omitempty"`
}

func coverageBoundRows(bounds []gomutant.CoverageBound) []coverageBoundOut {
	rows := make([]coverageBoundOut, 0, len(bounds))
	for _, b := range bounds {
		shown, omitted := capRows(b.Unreached)
		rows = append(rows, coverageBoundOut{Selection: b.Selection, Run: b.Run, Unreached: shown, OmittedUnreached: omitted})
	}
	return rows
}

type findingsOut struct {
	Summary                      []findingSummary                `json:"summary,omitempty" jsonschema:"the bounded default: one row per record"`
	Findings                     []inspectedFinding              `json:"findings,omitempty" jsonschema:"full rows, only under detail"`
	Omitted                      int                             `json:"omitted,omitempty" jsonschema:"rows beyond the cap - the document on disk always carries the full set"`
	RepoCommittable              int                             `json:"repoCommittable" jsonschema:"records portable enough for the committed findings document"`
	LocalOnly                    int                             `json:"localOnly" jsonschema:"records held in the machine-local overlay a reviewer would not inherit"`
	Document                     string                          `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	Note                         string                          `json:"note,omitempty" jsonschema:"set when there are no rows: says whether the document is empty or the filters matched nothing, and the next step"`
	EphemeralAttestations        []gomutant.EphemeralAttestation `json:"ephemeralAttestations,omitempty" jsonschema:"committed ephemeral-equivalence attestations beside the document - judged-equivalent manual probes with their edit digests and reasoning; capped, the record on disk carries the full set"`
	CoverageBounds               []coverageBoundOut              `json:"coverageBounds,omitempty" jsonschema:"the document's stated coverage bounds, one per declared build selection: the targets that selection's leg discovered but no oracle reaches - the population the measurement did not cover, never a silent zero; rows and each row's roster capped, the remainders counted"`
	OmittedCoverageBounds        int                             `json:"omittedCoverageBounds,omitempty" jsonschema:"coverage-bound rows beyond the response cap - counted, never silent; the document carries the full table"`
	OmittedEphemeralAttestations int                             `json:"omittedEphemeralAttestations,omitempty" jsonschema:"attestation rows beyond the response cap - counted, never silent"`
	LegacyOverlays               []gomutant.LegacyEntry          `json:"legacyOverlays,omitempty" jsonschema:"machine-local overlay entries preserved unread because their document version predates this binary's range: an older gomutant's records, attested dispositions included, never served and never deleted; capped, the overlay directory holds the full set"`
	OmittedLegacyOverlays        int                             `json:"omittedLegacyOverlays,omitempty" jsonschema:"legacy overlay rows beyond the response cap - counted, never silent"`
}

func (s *Server) toolFindings(ctx context.Context, req *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, findingsOut, error) {
	out := findingsOut{Document: gomutant.FindingsPathAt(s.dir, in.Findings)}
	// The state's spelling is refused before anything is read
	// (REQ-exec-preparation).
	if err := gomutant.ValidateFindingState(in.State); err != nil {
		return nil, out, err
	}
	// A changed ref's surface is read at preparation, in the tree root,
	// before any record is read (REQ-exec-preparation); the cut
	// resolves after the load.
	request, err := gomutant.PrepareSelection(ctx, s.dir, nil, s.targetInputs("", "", in.Changed), in.selection(), nil, nil)
	if err != nil {
		return nil, out, err
	}
	filter := gomutant.RecordFilter{
		Label:        in.Label,
		Symbol:       in.Symbol,
		Run:          in.Run,
		Selection:    gomutant.SelectionKey(in.selection()),
		HasSelection: len(in.Tags) > 0 || in.Toolchain != "",
	}
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return nil, out, err
	}
	// The committed ephemeral-equivalence record rides the inspection,
	// independent of the finding rows (REQ-result-ephemeral-attest).
	atts, err := gomutant.LoadEphemeralAttestations(gomutant.EphemeralAttestationsPathFor(out.Document))
	if err != nil {
		return nil, out, err
	}
	out.EphemeralAttestations, out.OmittedEphemeralAttestations = capRows(atts)
	// The document's stated coverage bounds ride the inspection, rows
	// and rosters capped like every list (REQ-result-unreached-bound,
	// REQ-mcp-envelope).
	bounds, err := store.CoverageBounds(ctx)
	if err != nil {
		return nil, out, err
	}
	out.CoverageBounds, out.OmittedCoverageBounds = capRows(coverageBoundRows(bounds))
	out.LegacyOverlays, out.OmittedLegacyOverlays = capRows(store.LegacyEntries())
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	// Zero rows is an answer with two different next steps - measure
	// first, or widen the filters - so the response says which
	// (REQ-mcp-envelope).
	if len(all) == 0 {
		out.Note = gomutant.NoRecordsNote(out.Document, len(all), 0, filter, in.State)
		return nil, out, nil
	}
	matched := gomutant.FilterRecords(all, filter)
	if len(matched) == 0 {
		out.Note = gomutant.NoRecordsNote(out.Document, len(all), 0, filter, in.State)
		return nil, out, nil
	}
	notify := progressNotifier(ctx, req)
	// The default path loads no tree at all: the document's recorded
	// facts answer without one, and the five-minute field inspections
	// were freshness re-derivation, never parsing.
	judge := in.judged()
	var tree *gomutant.Tree
	if judge || in.Changed != "" {
		tree, err = s.loadTreeReporting(ctx, notify, in.selection())
		if err != nil {
			return nil, out, err
		}
	}
	var cut *gomutant.DeltaCut
	if request.Changed != nil {
		selected, err := s.selectTargets(ctx, tree, gomutant.SelectionRequest{Changed: request.Changed, Cut: true})
		if err != nil {
			return nil, out, err
		}
		cut = selected.Cut
	}
	// One walk both faces render from (REQ-result-inspection), its
	// stages announced and naming the heartbeat's stretch — freshness
	// judging over a large document is minutes-class work, and a silent
	// stretch reads as a hang; the face projects its rows from the
	// walk's, the summary row carrying counts alone.
	seed := gomutant.StretchPreparation
	if tree != nil {
		seed = gomutant.StretchPreparing(loadingEvent)
	}
	inspection, err := inspectionHeartbeat(ctx, notify, seed, func(ctx context.Context, phase func(string)) (gomutant.Inspection, error) {
		return gomutant.InspectDocument(ctx, tree, store, matched, gomutant.InspectionRequest{Judge: judge, State: in.State, Cut: cut, Phase: phase})
	})
	if err != nil {
		return nil, out, err
	}
	out.RepoCommittable, out.LocalOnly = inspection.Repo, inspection.Local
	for _, row := range inspection.Rows {
		finding := row.Finding
		var onDelta []gomutant.Survivor
		var deltaCount *int
		if row.Delta != nil {
			onDelta = append([]gomutant.Survivor{}, row.Delta.OnDelta...)
			n := len(onDelta)
			deltaCount = &n
		}
		if !in.Detail {
			out.Summary = append(out.Summary, findingSummary{
				oracleExecutionPolicyOut: oracleExecutionPolicyRow(finding),
				Symbol:                   finding.Symbol, Selection: finding.Selection, State: row.Inspection.State, Reason: row.Inspection.Reason,
				Layer: row.Layer, Run: finding.Run, Open: len(finding.Open()), DeltaOpen: deltaCount, Attested: len(finding.AttestedDispositions()),
				Withdrawn: len(finding.WithdrawnDispositions()),
			})
			continue
		}
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		out.Findings = append(out.Findings, inspectedFinding{
			oracleExecutionPolicyOut: oracleExecutionPolicyRow(finding),
			Symbol:                   finding.Symbol, Selection: finding.Selection, Labels: labels, State: row.Inspection.State, Reason: row.Inspection.Reason,
			Layer: row.Layer, LayerReason: row.LayerReason, Run: finding.Run, DeltaOpen: onDelta,
			CandidateCount: finding.CandidateCount, Generated: finding.Generated,
			Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
			Withdrawn:  append([]gomutant.WithdrawnAttestation(nil), finding.WithdrawnDispositions()...),
			Candidates: row.Inspection.CandidateEvidence,
		})
	}
	// The state filter drops rows during judging, after the earlier
	// zero-match returns - its empty answer names itself the same way.
	if len(inspection.Rows) == 0 {
		out.Note = gomutant.NoRecordsNote(out.Document, len(all), len(matched), filter, in.State)
	}
	var omittedSummary, omittedFindings int
	out.Summary, omittedSummary = capRows(out.Summary)
	out.Findings, omittedFindings = capRows(out.Findings)
	out.Omitted = omittedSummary + omittedFindings
	return nil, out, nil
}

type explainIn struct {
	selectionIn
	Symbol   string `json:"symbol,omitempty"`
	Label    string `json:"label,omitempty"`
	Findings string `json:"findings,omitempty"`
}

type promotionGroup struct {
	Reason         string   `json:"reason"`
	Count          int      `json:"count"`
	Symbols        []string `json:"symbols"`
	OmittedSymbols int      `json:"omittedSymbols,omitempty"`
}

type explainOut struct {
	Symbol              string                       `json:"symbol,omitempty"`
	Selection           string                       `json:"selection,omitempty"`
	State               gomutant.FindingState        `json:"state,omitempty"`
	Reason              string                       `json:"reason,omitempty"`
	Layer               string                       `json:"layer,omitempty"`
	LayerReasons        []string                     `json:"layerReasons,omitempty" jsonschema:"every portable-line clause the record fails, not only the first"`
	OmittedLayerReasons int                          `json:"omittedLayerReasons,omitempty"`
	Open                []gomutant.ExplainedSurvivor `json:"open,omitempty"`
	OmittedOpen         int                          `json:"omittedOpen,omitempty"`
	Attested            int                          `json:"attested,omitempty"`
	Withdrawn           int                          `json:"withdrawn,omitempty"`
	RepoCommittable     *int                         `json:"repoCommittable,omitempty" jsonschema:"document arm: records portable enough for the committed findings document"`
	Document            string                       `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	LocalOnly           *int                         `json:"localOnly,omitempty" jsonschema:"document arm: records held in the machine-local overlay"`
	Promotion           []promotionGroup             `json:"promotion,omitempty" jsonschema:"document arm: machine-local records grouped by failing portable-line clause"`
	Note                string                       `json:"note,omitempty" jsonschema:"set when the document arm has nothing to group: says whether the document is empty or the label matched nothing, and the next step"`
	OmittedGroups       int                          `json:"omittedGroups,omitempty"`
}

// toolExplain answers why: a symbol's record explained causally (state,
// full machine-local clause list, per-survivor prescriptions), or the
// document's promotion triage. Projection only - no tests run, and the
// advisory stance holds: causes and prescriptions, never a verdict.
func (s *Server) toolExplain(ctx context.Context, req *mcp.CallToolRequest, in explainIn) (*mcp.CallToolResult, explainOut, error) {
	if in.Symbol != "" && in.Label != "" {
		return nil, explainOut{}, fmt.Errorf("explain: the label filter restricts the triage arm; pass symbol or label, not both")
	}
	docPath := gomutant.FindingsPathAt(s.dir, in.Findings)
	store, err := gomutant.OpenStore(docPath, s.dir)
	if err != nil {
		return nil, explainOut{}, err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return nil, explainOut{}, err
	}
	hasSel := len(in.Tags) > 0 || in.Toolchain != ""
	if in.Symbol != "" {
		if err := ctx.Err(); err != nil {
			return nil, explainOut{}, err
		}
		finding, matches := gomutant.SelectExplainFinding(all, in.Symbol, in.selection(), hasSel)
		if matches > 1 {
			return nil, explainOut{}, fmt.Errorf("explain: multiple finding records for %s across build selections; specify tags or toolchain", in.Symbol)
		}
		if matches == 1 {
			notify := progressNotifier(ctx, req)
			loadSel := gomutant.AttestedLoadSelection(in.selection(), hasSel, finding)
			tree, err := s.loadTreeReporting(ctx, notify, loadSel)
			if err != nil {
				return nil, explainOut{}, err
			}
			// Freshness judging can be minutes-class; the walk's stages
			// are announced and ride the heartbeat so it never reads as
			// a hang.
			inspection, err := inspectionHeartbeat(ctx, notify, gomutant.StretchPreparing(loadingEvent), func(ctx context.Context, phase func(string)) (gomutant.FindingInspection, error) {
				return tree.InspectFinding(ctx, finding, phase)
			})
			if err != nil {
				return nil, explainOut{}, err
			}
			explained := store.ExplainSymbolFinding(finding, inspection)
			out := explainOut{
				Symbol:       explained.Symbol,
				Selection:    explained.Selection,
				State:        explained.State,
				Reason:       explained.Reason,
				Layer:        explained.Layer,
				LayerReasons: explained.LayerReasons,
				Open:         explained.Open,
				Attested:     explained.Attested,
				Withdrawn:    explained.Withdrawn,
				Document:     docPath,
			}
			if len(out.LayerReasons) > envelope.reasons {
				out.OmittedLayerReasons = len(out.LayerReasons) - envelope.reasons
				out.LayerReasons = out.LayerReasons[:envelope.reasons]
			}
			if len(out.Open) > envelope.open {
				out.OmittedOpen = len(out.Open) - envelope.open
				out.Open = out.Open[:envelope.open]
			}
			return nil, out, nil
		}
		return nil, explainOut{}, fmt.Errorf("explain: no finding for %s; the findings tool lists the recorded symbols", in.Symbol)
	}
	triage, err := store.ExplainTriage(ctx, all, gomutant.RecordFilter{
		Label:        in.Label,
		Selection:    gomutant.SelectionKey(in.selection()),
		HasSelection: hasSel,
	}, docPath)
	if err != nil {
		return nil, explainOut{}, err
	}
	repo, local := triage.RepoCommittable, triage.LocalOnly
	out := explainOut{RepoCommittable: &repo, LocalOnly: &local, Document: docPath, Note: triage.Note}
	for i, g := range triage.Promotion {
		if i == envelope.rows {
			out.OmittedGroups = len(triage.Promotion) - envelope.rows
			break
		}
		group := promotionGroup{Reason: g.Reason, Count: g.Count}
		if len(g.Symbols) > envelope.nested {
			group.Symbols = g.Symbols[:envelope.nested]
			group.OmittedSymbols = len(g.Symbols) - envelope.nested
		} else {
			group.Symbols = g.Symbols
		}
		out.Promotion = append(out.Promotion, group)
	}
	return nil, out, nil
}
