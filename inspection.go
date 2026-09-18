package gomutant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ValidateFindingState refuses an inspection state that is not a
// judged state or empty — decidable at the request, before any store
// opens (REQ-exec-preparation).
func ValidateFindingState(state string) error {
	switch state {
	case "", string(FindingCurrent), string(FindingStale), string(FindingUnverifiable), string(FindingDetached):
		return nil
	}
	return fmt.Errorf("unknown state %q (current, stale, unverifiable, detached)", state)
}

// FilterRecords keeps the records the filter admits, in document order
// — the match a face notes on before it decides whether a tree loads.
func FilterRecords(all []Finding, filter RecordFilter) []Finding {
	matched := make([]Finding, 0, len(all))
	for _, f := range all {
		if filter.Admits(f) {
			matched = append(matched, f)
		}
	}
	return matched
}

// InspectionRequest is one face's inspection of matched records: judge
// re-derives each record's freshness against the tree (the expensive
// truth; the default reads the recorded facts and loads no tree), State
// keeps only records judged in that state, Cut places each record's
// open survivors against a changed ref's delta (through the tree,
// deriving no freshness), and Phase hears the judging stretch.
type InspectionRequest struct {
	Judge bool
	State string
	Cut   *DeltaCut
	Phase func(string)
}

// InspectedRecord is one inspected record as data: the record, its
// inspection, its persistence layer with the disqualifier when
// machine-local, and — exactly when the cut ran — its open survivors
// split by the delta.
type InspectedRecord struct {
	Finding     Finding
	Inspection  FindingInspection
	Layer       string
	LayerReason string
	Delta       *DeltaSurvivors
}

// Inspection is the walk's answer: the rows kept, sorted by symbol,
// and the layer counts over them.
type Inspection struct {
	Rows  []InspectedRecord
	Repo  int
	Local int
}

// InspectDocument is the one inspection walk both faces render from
// (REQ-result-inspection): one pass over the matched records' shared
// subject views when judging, the state filter after the judgment,
// the layer per record, the cut per record when one was given; tree
// may be nil when neither judging nor cutting.
func InspectDocument(ctx context.Context, tree *Tree, store *Store, matched []Finding, req InspectionRequest) (Inspection, error) {
	var result Inspection
	if tree == nil && (req.Judge || req.Cut != nil) {
		return result, errors.New("gomutant: inspection asked to judge or cut without a tree")
	}
	phase := req.Phase
	if phase == nil {
		phase = func(string) {}
	}
	phase(fmt.Sprintf("reading %d record(s)", len(matched)))
	inspections := make([]FindingInspection, len(matched))
	for i, f := range matched {
		inspections[i] = RecordedInspection(f)
	}
	if req.Judge && len(matched) > 0 {
		phase(fmt.Sprintf("judging %d record(s)", len(matched)))
		judged, err := tree.InspectFindings(ctx, matched, phase)
		if err != nil {
			return result, err
		}
		inspections = judged
	}
	for i, f := range matched {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		inspection := inspections[i]
		if req.State != "" && string(inspection.State) != req.State {
			continue
		}
		row := InspectedRecord{Finding: f, Inspection: inspection}
		row.Layer, row.LayerReason = store.Layer(f)
		if row.Layer == "repo" {
			result.Repo++
		} else {
			result.Local++
		}
		if req.Cut != nil {
			split, err := tree.CutSurvivorsContext(ctx, f, *req.Cut)
			if err != nil {
				return result, err
			}
			row.Delta = &split
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Finding.Symbol < result.Rows[j].Finding.Symbol })
	return result, nil
}

// NoRecordsNote is the zero-row answer both faces give: which input
// emptied the roster — no record at all, the record filters given, or
// the state — and the caller's next step (REQ-result-inspection,
// REQ-mcp-envelope).
func NoRecordsNote(document string, recorded, matched int, filter RecordFilter, state string) string {
	switch {
	case recorded == 0:
		return "no findings recorded at " + document + " - run measures the tree first"
	case matched == 0:
		given, noun, pronoun := filter.Given(), "filters", "them"
		if !strings.Contains(given, "/") {
			noun, pronoun = "filter", "it"
		}
		return fmt.Sprintf("the %s %s matched none of %d recorded finding(s); drop %s to list the document", given, noun, recorded, pronoun)
	case state != "":
		return fmt.Sprintf("state=%s matched none of the %d finding(s) the other filters kept; drop it to list them", state, matched)
	}
	return ""
}

// Given names the filters the request set, in the one order both faces
// spell: label, symbol, run.
func (f RecordFilter) Given() string {
	var given []string
	if f.Label != "" {
		given = append(given, "label")
	}
	if f.Symbol != "" {
		given = append(given, "symbol")
	}
	if f.Run != "" {
		given = append(given, "run")
	}
	return strings.Join(given, "/")
}
