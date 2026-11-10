package mcpserver

import (
	"context"
	"fmt"

	"github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type attestIn struct {
	selectionIn
	Symbol   string `json:"symbol"`
	Position string `json:"position"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
	Reattest bool   `json:"reattest,omitempty"`
	Withdraw bool   `json:"withdraw,omitempty"`
	Findings string `json:"findings,omitempty"`
}

// attestedEcho restates a recorded disposition in structured fields —
// operators contain spaces, so a joined string would be unparseable.
type attestedEcho struct {
	Symbol    string `json:"symbol"`
	Selection string `json:"selection,omitempty"`
	Position  string `json:"position"`
	Operator  string `json:"operator"`
	Withdrawn bool   `json:"withdrawn,omitempty"`
}

type attestOut struct {
	Recorded                 *attestedEcho               `json:"recorded,omitempty" jsonschema:"the disposition as recorded, echoed so the write is confirmed, not inferred"`
	Open                     int                         `json:"open" jsonschema:"the symbol's open findings after the disposition"`
	Layer                    string                      `json:"layer" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay"`
	LayerReason              string                      `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
	Posture                  gomutant.RecordPosture      `json:"posture" jsonschema:"the record's reuse posture judged once under this call's selection: reusable as it stands or not, each refusing channel named, and what a later judgment needs — a disposition is never reusable evidence by itself"`
	ExemptionsRekeyed        []gomutant.RekeyedExemption `json:"exemptionsRekeyed,omitempty" jsonschema:"reviewed exemption entries whose clause this call's write re-keyed to canonical path or bracket-root spelling - the subject, the clause as it was, the clause as it matches now; present exactly when the record was rewritten"`
	OmittedExemptionsRekeyed int                         `json:"omittedExemptionsRekeyed,omitempty" jsonschema:"re-keyed entries beyond the response cap - counted, not listed"`
}

func (s *Server) toolAttest(ctx context.Context, req *mcp.CallToolRequest, in attestIn) (*mcp.CallToolResult, attestOut, error) {
	var out attestOut
	for _, f := range map[string]string{"symbol": in.Symbol, "position": in.Position, "operator": in.Operator, "reason": in.Reason} {
		if f == "" {
			return nil, out, fmt.Errorf("attest_survivor needs symbol, position, operator, and reason")
		}
	}
	if in.Reattest && in.Withdraw {
		return nil, out, fmt.Errorf("attest_survivor cannot combine reattest and withdraw")
	}
	// The reasoning's shape is a declaration's: refused before the
	// document lock persists anything (REQ-exec-preparation).
	if err := gomutant.ValidateAttestationReason(in.Reason); err != nil {
		return nil, out, err
	}
	// The root's existence, the standing vouch set's shape, and the
	// load ladder refuse before the write, as on the CLI
	// (REQ-exec-preparation, REQ-exec-provenance).
	sel := in.selection()
	if err := gomutant.PrepareAttestation(ctx, s.dir, sel); err != nil {
		return nil, out, err
	}
	var attested gomutant.Finding
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	hasSel := len(in.Tags) > 0 || in.Toolchain != ""
	routing, err := s.updateStore(ctx, store, gomutant.FindingsPathAt(s.dir, in.Findings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		var err error
		all, attested, err = gomutant.AttestFindingWithOptions(all, in.Symbol, in.Position, in.Operator, in.Reason, gomutant.AttestOptions{
			Selection:    gomutant.SelectionKey(sel),
			HasSelection: hasSel,
			Reattest:     in.Reattest,
			Withdraw:     in.Withdraw,
		})
		if err != nil {
			return nil, err
		}
		out.Open = len(attested.Open())
		return all, nil
	})
	if err != nil {
		return nil, out, err
	}
	out.Recorded = &attestedEcho{Symbol: in.Symbol, Selection: attested.Selection, Position: in.Position, Operator: in.Operator, Withdrawn: in.Withdraw}
	// The echo states where the record lives and whether it can serve
	// as it stands: a disposition on a record whose pins moved is
	// judged afresh - and shed if rejected - by the next measure
	// (REQ-attest-survivor). The disposition is already recorded, so
	// the tree load and inspection below demote their failures to
	// warnings - a hard error there would read as a failed write that
	// in fact landed.
	out.Layer, out.LayerReason = routing.Of(store, attested)
	out.ExemptionsRekeyed, out.OmittedExemptionsRekeyed = rekeyedRows(store.RekeyedExemptions())
	notify := progressNotifier(ctx, req)
	loadSel := gomutant.AttestedLoadSelection(sel, hasSel, attested)
	tree, err := s.loadTreeReporting(ctx, notify, loadSel)
	if err != nil {
		out.Posture = gomutant.RecordedPosture(attested, gomutant.FindingInspection{}, err)
		return nil, out, nil
	}
	inspection, err := inspectionHeartbeat(ctx, notify, gomutant.StretchPreparing(loadingEvent), func(ctx context.Context, phase func(string)) (gomutant.FindingInspection, error) {
		return tree.InspectFinding(ctx, attested, phase)
	})
	out.Posture = gomutant.RecordedPosture(attested, inspection, err)
	return nil, out, nil
}

type pruneIn struct {
	selectionIn
	Check               bool   `json:"check,omitempty"`
	Cache               bool   `json:"cache,omitempty"`
	AllTrees            bool   `json:"all_trees,omitempty"`
	MaxBytes            *int64 `json:"max_bytes,omitempty"`
	IncludeLocalRecords bool   `json:"include_local_records,omitempty"`
	IncludeAttested     bool   `json:"include_attested,omitempty"`
	Findings            string `json:"findings,omitempty"`
}

type prunedOut struct {
	Symbol    string                 `json:"symbol"`
	Selection string                 `json:"selection,omitempty"`
	Layer     string                 `json:"layer" jsonschema:"repo when the record sat in the findings document, local when it sat in the machine-local overlay — a symbol held in both layers is two rows"`
	Attested  []gomutant.Attestation `json:"attested,omitempty" jsonschema:"the removed record's dispositions, echoed so the reasoning survives the removal"`
}

type pruneOut struct {
	Removed                  []prunedOut                           `json:"removed" jsonschema:"never truncated - for overlay-resident records this echo is the disposition reasoning's last home"`
	RemovedEphemeral         []gomutant.PrunedEphemeralAttestation `json:"removedEphemeral,omitempty" jsonschema:"committed ephemeral-equivalence attestations whose testPkg no longer resolves in the tree, echoed in full"`
	Kept                     layerCountsOut                        `json:"kept" jsonschema:"records kept, counted per layer: repo the committed document's rows, local the machine-local overlay's"`
	Cache                    *gomutant.CachePruneResult            `json:"cache,omitempty" jsonschema:"present when cache=true: summary of reclaimed baseline banks and gofresh memos and preserved local overlay records"`
	Check                    bool                                  `json:"check,omitempty"`
	Document                 string                                `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	ExemptionsRekeyed        []gomutant.RekeyedExemption           `json:"exemptionsRekeyed,omitempty" jsonschema:"reviewed exemption entries whose clause this call's write re-keyed to canonical path or bracket-root spelling - the subject, the clause as it was, the clause as it matches now; present exactly when the record was rewritten"`
	OmittedExemptionsRekeyed int                                   `json:"omittedExemptionsRekeyed,omitempty" jsonschema:"re-keyed entries beyond the response cap - counted, not listed"`
}

func (s *Server) toolPrune(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, pruneOut, error) {
	out := pruneOut{Removed: []prunedOut{}, Document: gomutant.FindingsPathAt(s.dir, in.Findings)}
	if !in.Cache && (in.AllTrees || in.MaxBytes != nil || in.IncludeLocalRecords || in.IncludeAttested) {
		return nil, out, fmt.Errorf("prune: all_trees, max_bytes, include_local_records, and include_attested require cache=true")
	}
	if in.Cache {
		var maxBytes int64
		if in.MaxBytes != nil {
			maxBytes = *in.MaxBytes
		}
		res, err := gomutant.PruneCacheContext(ctx, s.dir, gomutant.CachePruneOptions{
			Check:               in.Check,
			AllTrees:            in.AllTrees,
			MaxBytes:            maxBytes,
			IncludeLocalRecords: in.IncludeLocalRecords,
			IncludeAttested:     in.IncludeAttested,
		})
		if err != nil {
			return nil, out, err
		}
		out.Cache = &res
		out.Check = res.Check
		return nil, out, nil
	}
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	tree, err := s.loadTreeReporting(ctx, notify, in.selection())
	if err != nil {
		return nil, out, err
	}
	result, err := tree.PruneDetachedContext(ctx, store, in.Check)
	if err != nil {
		return nil, out, err
	}
	out.Kept, out.Check = layerCountsRow(result.Kept), result.Check
	out.RemovedEphemeral = result.RemovedEphemeral
	out.ExemptionsRekeyed, out.OmittedExemptionsRekeyed = rekeyedRows(result.ExemptionsRekeyed)
	// The removal echo is never truncated: for an overlay-resident
	// record the response is the disposition reasoning's last home, and
	// a capped check preview would hide part of what a destructive call
	// is about to delete (REQ-mcp-lifecycle's envelope exception).
	for _, record := range result.Removed {
		out.Removed = append(out.Removed, prunedRow(record))
	}
	return nil, out, nil
}

type retargetIn struct {
	selectionIn
	From     string `json:"from"`
	To       string `json:"to"`
	Check    bool   `json:"check,omitempty"`
	Findings string `json:"findings,omitempty"`
}

// rewrittenOut is one record whose mutated symbol a retarget rewrote,
// in its own layer.
type rewrittenOut struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Selection string `json:"selection,omitempty"`
	Layer     string `json:"layer" jsonschema:"repo when the record sits in the findings document, local when it sits in the machine-local overlay — a symbol held in both layers is two rows"`
	Shadowed  bool   `json:"shadowed,omitempty" jsonschema:"a document row whose new symbol the machine-local overlay holds once the retarget has written: every read serves the overlay's record until that entry leaves"`
}

// touchedOut is one field rewrite on a record whose own symbol stayed.
type touchedOut struct {
	Record string `json:"record"`
	Layer  string `json:"layer" jsonschema:"the touched record's layer: repo or local"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// layerCountsOut is LayerCounts on the wire: one integer per layer of
// REQ-result-layers, the total the reader's sum.
type layerCountsOut struct {
	Repo  int `json:"repo" jsonschema:"records in the committed findings document"`
	Local int `json:"local" jsonschema:"records in the machine-local overlay"`
}

// layerCountsRow projects LayerCounts field by field.
func layerCountsRow(c gomutant.LayerCounts) layerCountsOut {
	return layerCountsOut{Repo: c.Repo, Local: c.Local}
}

// rewrittenExemptionOut is RewrittenExemption on the wire.
type rewrittenExemptionOut struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// rekeyedRows is every committing verb's listing of the exemption
// entries its write re-keyed — the library's own row, served as it is
// — capped by the envelope's row rule (REQ-result-exemptions,
// REQ-mcp-envelope).
func rekeyedRows(rekeyed []gomutant.RekeyedExemption) ([]gomutant.RekeyedExemption, int) {
	return capRows(rekeyed)
}

// prunedRow, rewrittenRow, and touchedRow are the lifecycle records'
// wire projections — every field copied by name, the projection pin
// comparing values through them.
func prunedRow(r gomutant.PrunedRecord) prunedOut {
	return prunedOut{Symbol: r.Symbol, Selection: r.Selection, Layer: r.Layer, Attested: r.Attested}
}

func rewrittenRow(r gomutant.RetargetedRecord) rewrittenOut {
	return rewrittenOut{From: r.From, To: r.To, Selection: r.Selection, Layer: r.Layer, Shadowed: r.Shadowed}
}

func touchedRow(m gomutant.TouchedRewrite) touchedOut {
	return touchedOut{Record: m.Record, Layer: m.Layer, From: m.From, To: m.To}
}

type retargetOut struct {
	Rewritten                []rewrittenOut                           `json:"rewritten" jsonschema:"records whose mutated symbol changed, each in its own layer"`
	RewrittenCounts          layerCountsOut                           `json:"rewrittenCounts" jsonschema:"records whose own symbol the rename rewrote, counted per layer (repo, local) - the count leads where the rewrites list is capped"`
	Touched                  layerCountsOut                           `json:"touched" jsonschema:"records the rename's closure updated without renaming their own symbol (an oracle or killer in the renamed surface) - counted per layer (repo, local)"`
	TouchedRewrites          []touchedOut                             `json:"touchedRewrites,omitempty" jsonschema:"the touched records' field rewrites - the surface no resolution gate reaches, echoed for audit"`
	OmittedRewritten         int                                      `json:"omittedRewritten,omitempty" jsonschema:"rewritten rows beyond the response cap - counted, not listed; under check they are previews"`
	OmittedTouched           int                                      `json:"omittedTouched,omitempty" jsonschema:"touched rewrite rows beyond the response cap - counted, not listed"`
	Check                    bool                                     `json:"check,omitempty"`
	Document                 string                                   `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	Exemptions               []rewrittenExemptionOut                  `json:"exemptions,omitempty" jsonschema:"reviewed exemption entries whose subjects the rename moved, rewritten with the records - the reason and rationale untouched; capped at 50"`
	OmittedExemptions        int                                      `json:"omittedExemptions,omitempty" jsonschema:"rewritten exemption subjects beyond the response cap - counted, not listed"`
	Ephemeral                []gomutant.RewrittenEphemeralAttestation `json:"ephemeral,omitempty" jsonschema:"committed ephemeral-equivalence attestations whose testPkg moved under the rename, rewritten with the records; capped at 50"`
	OmittedEphemeral         int                                      `json:"omittedEphemeral,omitempty" jsonschema:"rewritten ephemeral attestations beyond the response cap - counted, not listed"`
	Note                     string                                   `json:"note,omitempty" jsonschema:"set when the rename touched nothing: no record and no reviewed exemption subject moved, and the findings tool lists the recorded symbols"`
	ExemptionsRekeyed        []gomutant.RekeyedExemption              `json:"exemptionsRekeyed,omitempty" jsonschema:"reviewed exemption entries whose clause this call's write re-keyed to canonical path or bracket-root spelling - the subject, the clause as it was, the clause as it matches now; present exactly when the record was rewritten"`
	OmittedExemptionsRekeyed int                                      `json:"omittedExemptionsRekeyed,omitempty" jsonschema:"re-keyed entries beyond the response cap - counted, not listed"`
}

func (s *Server) toolRetarget(ctx context.Context, req *mcp.CallToolRequest, in retargetIn) (*mcp.CallToolResult, retargetOut, error) {
	out := retargetOut{Rewritten: []rewrittenOut{}, Document: gomutant.FindingsPathAt(s.dir, in.Findings)}
	if err := gomutant.ValidateRetargetPair(in.From, in.To); err != nil {
		return nil, out, err
	}
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	tree, err := s.loadTreeReporting(ctx, notify, in.selection())
	if err != nil {
		return nil, out, err
	}
	result, err := tree.RetargetContext(ctx, store, in.From, in.To, in.Check)
	if err != nil {
		return nil, out, err
	}
	out.Check = result.Check
	out.RewrittenCounts = layerCountsRow(result.RewrittenCounts)
	out.Touched = layerCountsRow(result.Touched)
	for _, r := range result.Rewritten {
		out.Rewritten = append(out.Rewritten, rewrittenRow(r))
	}
	for _, m := range result.TouchedRewrites {
		out.TouchedRewrites = append(out.TouchedRewrites, touchedRow(m))
	}
	for _, e := range result.Exemptions {
		out.Exemptions = append(out.Exemptions, rewrittenExemptionOut{From: e.From, To: e.To})
	}
	out.Exemptions, out.OmittedExemptions = capRows(out.Exemptions)
	out.Ephemeral, out.OmittedEphemeral = capRows(result.Ephemeral)
	out.ExemptionsRekeyed, out.OmittedExemptionsRekeyed = rekeyedRows(result.ExemptionsRekeyed)
	// A rename that moved nothing is an answer with a next step, not an
	// empty success: the prefix either mismatches the recorded spelling
	// or the rewrite already landed (REQ-mcp-envelope).
	if len(result.Rewritten) == 0 && result.Touched.Total() == 0 && len(result.Exemptions) == 0 && len(result.Ephemeral) == 0 {
		out.Note = fmt.Sprintf("prefix %q matched no records; the findings tool lists the recorded symbols", in.From)
	}
	out.Rewritten, out.OmittedRewritten = capRows(out.Rewritten)
	out.TouchedRewrites, out.OmittedTouched = capRows(out.TouchedRewrites)
	return nil, out, nil
}

type ephemeralIn struct {
	selectionIn
	File            string               `json:"file,omitempty"`
	Replacement     string               `json:"replacement,omitempty"`
	Edits           []gomutant.Edit      `json:"edits,omitempty"`
	BatchEdits      []gomutant.BatchEdit `json:"batch_edits,omitempty"`
	TestPkg         string               `json:"test_pkg"`
	Run             string               `json:"run"`
	TimeoutSec      *int                 `json:"timeout_sec,omitempty"`
	OracleMemoryMiB *int64               `json:"oracle_memory_mib,omitempty"`
	Runs            int                  `json:"runs,omitempty"`
	Reattest        bool                 `json:"reattest,omitempty"`
	Attest          string               `json:"attest,omitempty"`
	Findings        string               `json:"findings,omitempty"`
}

// ephemeralOut is the probe result plus the attestation confirmation
// when attest was requested.
type ephemeralOut struct {
	*gomutant.EphemeralResult
	AttestationRecorded string `json:"attestationRecorded,omitempty" jsonschema:"edit digest of the equivalence attestation appended to the committed record"`
	AttestationPath     string `json:"attestationPath,omitempty" jsonschema:"the committed record the attestation was written to"`
}

func (s *Server) toolEphemeral(ctx context.Context, req *mcp.CallToolRequest, in ephemeralIn) (*mcp.CallToolResult, *ephemeralOut, error) {
	// The probe's bounds are its own value, derived once inside the
	// request from the caller's memory CHOICE — absent for the derived
	// default, a MiB count, -1 for unlimited — and handed to every
	// oracle spawn it makes, never process state a campaign or a sibling
	// probe could move (REQ-exec-oracle-memory; the width, a lone tree's,
	// is derived in the request beside the ceiling,
	// REQ-exec-oracle-parallelism). The choice crosses as the choice, as
	// the CLI passes it: a value derived here and derived again there
	// read -1 as the default ceiling.
	timeout, err := commandTimeout("timeout_sec", in.TimeoutSec)
	if err != nil {
		return nil, nil, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if in.TestPkg == "" || in.Run == "" {
		return nil, nil, fmt.Errorf("ephemeral needs test_pkg and run")
	}
	if err := gomutant.ValidateEphemeralRuns(in.Runs); err != nil {
		return nil, nil, err
	}
	if in.Attest != "" {
		if err := gomutant.ValidateAttestationReason(in.Attest); err != nil {
			return nil, nil, err
		}
	}
	forms := 0
	if in.Replacement != "" {
		forms++
	}
	if len(in.Edits) != 0 {
		forms++
	}
	if len(in.BatchEdits) != 0 {
		forms++
	}
	if forms != 1 {
		return nil, nil, fmt.Errorf("give replacement, edits, or batch_edits, exactly one")
	}
	if len(in.BatchEdits) == 0 {
		if in.File == "" {
			return nil, nil, fmt.Errorf("replacement and edits need file")
		}
		if err := localPath("file", in.File); err != nil {
			return nil, nil, err
		}
	} else {
		if in.File != "" {
			return nil, nil, fmt.Errorf("batch_edits carries its own files; omit file")
		}
		for i, edit := range in.BatchEdits {
			if err := localPath(fmt.Sprintf("batch_edits[%d].file", i), edit.File); err != nil {
				return nil, nil, err
			}
		}
	}
	notify := progressNotifier(ctx, req)
	tree, err := s.loadTreeReporting(ctx, notify, in.selection())
	if err != nil {
		return nil, nil, err
	}
	if notify != nil {
		notify("running " + in.TestPkg)
	}
	// The probe's phases ride the notifications as preparation events
	// and name the heartbeat's stretch (REQ-exec-run-status).
	// The label until the probe names its first phase is the load's —
	// the last stretch this call named.
	phase := newStretchLabel(gomutant.StretchPreparing(loadingEvent))
	probe := gomutant.EphemeralRequest{Findings: gomutant.FindingsPathAt(s.dir, in.Findings), RefuseAttested: in.Attest != "" && !in.Reattest, File: in.File, TestPkg: in.TestPkg, Run: in.Run, Runs: in.Runs, OracleMemoryBytes: mcpOracleMemoryBytes(in.OracleMemoryMiB), Progress: func(event gomutant.PreparationEvent) {
		phase.set(gomutant.StretchPreparing(event))
		if notify != nil {
			notify(preparationMessage(event))
		}
	}}
	switch {
	case len(in.BatchEdits) > 0:
		probe.BatchEdits = in.BatchEdits
	case len(in.Edits) > 0:
		probe.Edits = in.Edits
	default:
		probe.Mutant = []byte(in.Replacement)
	}
	res, err := withHeartbeatLabel(ctx, notify, phase.get, func(ctx context.Context) (*gomutant.EphemeralResult, error) {
		return tree.RunEphemeral(ctx, probe)
	})
	if err != nil {
		return nil, nil, err
	}
	out := &ephemeralOut{EphemeralResult: res}
	if in.Attest != "" {
		att, path, err := gomutant.CommitEphemeralEquivalence(ctx, s.dir, in.Findings, res, in.Attest, in.Reattest)
		if err != nil {
			return nil, nil, err
		}
		out.AttestationRecorded = att.EditDigest
		out.AttestationPath = path
	}
	return nil, out, nil
}
