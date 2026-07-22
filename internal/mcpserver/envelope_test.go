package mcpserver

import (
	"reflect"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

func reflectTypeOf(v any) reflect.Type { return reflect.TypeOf(v) }

// With a progress token the preparation and decision streams ride
// notifications and leave the response, their totals remaining counts;
// without one they stay inline, capped with the totals still honest
// (REQ-mcp-envelope).
func TestRunStreamsLeaveThePayloadWhenStreamed(t *testing.T) {
	var out runOut
	var notes []string
	streamed := newRunStreams(&out, func(m string) { notes = append(notes, m) })
	for range 3 {
		streamed.progress(gomutant.PreparationEvent{Stage: gomutant.PreparationMutants, Symbol: "p.F"})
		streamed.decision(gomutant.RunDecision{Symbol: "p.F", Action: "measure"})
	}
	if len(out.Preparation) != 0 || len(out.Decisions) != 0 {
		t.Fatalf("streamed rows stayed inline: %d preparation, %d decisions", len(out.Preparation), len(out.Decisions))
	}
	if out.PreparationCount != 3 || out.DecisionsCount != 3 || len(notes) != 6 {
		t.Fatalf("streamed totals = %d/%d, notes %d", out.PreparationCount, out.DecisionsCount, len(notes))
	}
	if phase, _ := streamed.lastPhase.Load().(string); phase != "executing mutants" {
		t.Fatalf("heartbeat phase = %q", phase)
	}

	var inline runOut
	collector := newRunStreams(&inline, nil)
	for range streamRowCap + 7 {
		collector.decision(gomutant.RunDecision{Symbol: "p.F", Action: "measure"})
	}
	if len(inline.Decisions) != streamRowCap || inline.DecisionsCount != streamRowCap+7 {
		t.Fatalf("inline cap = %d rows, %d total", len(inline.Decisions), inline.DecisionsCount)
	}
}

// Discover leads with counts and caps rows unless detail is requested
// (REQ-mcp-envelope): the count fields precede every row field in the
// serialized response by declaration order.
func TestDiscoverCountsLeadAndRowsCap(t *testing.T) {
	s := New(fixtureDir)
	_, out, err := s.toolDiscover(t.Context(), nil, discoverIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.TargetCount == 0 || out.TargetCount != len(out.Targets)+out.OmittedTargets {
		t.Fatalf("counts = %d targets, %d rows, %d omitted", out.TargetCount, len(out.Targets), out.OmittedTargets)
	}
	// The fixture overflows the cap, so the default response must
	// genuinely truncate: the cap's removal is a failure, not a no-op.
	if len(out.Targets) != 50 || out.OmittedTargets == 0 {
		t.Fatalf("default rows = %d, omitted %d; want the cap engaged", len(out.Targets), out.OmittedTargets)
	}
	_, detail, err := s.toolDiscover(t.Context(), nil, discoverIn{Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	if detail.OmittedTargets != 0 || len(detail.Targets) != detail.TargetCount {
		t.Fatalf("detail rows = %d of %d, omitted %d", len(detail.Targets), detail.TargetCount, detail.OmittedTargets)
	}
}

// The run response never inlines candidate evidence: it is drill-down
// via the findings tool (REQ-mcp-envelope).
func TestRunResponseCarriesNoCandidateEvidenceField(t *testing.T) {
	fields := strings.Join(structJSONTags(findingOut{}), ",")
	if strings.Contains(fields, "candidateEvidence") {
		t.Fatalf("run finding row fields = %s; candidate evidence is drill-down", fields)
	}
}

func structJSONTags(v any) []string {
	t := reflectTypeOf(v)
	tags := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tags = append(tags, t.Field(i).Tag.Get("json"))
	}
	return tags
}

// Guidance aggregates per oracle set: targets sharing one unstable
// oracle share one entry instead of repeating the suggestion
// (REQ-mcp-envelope).
func TestGuidanceAggregatesPerOracleSet(t *testing.T) {
	var entries []guidanceOut
	shared := gomutant.OracleGuidance{UnstableTests: []string{"p.TestU"}, Reason: "sealed", Suggestion: "narrow"}
	for _, sym := range []string{"p.A", "p.B", "p.C"} {
		g := shared
		g.Symbol = sym
		appendGuidance(&entries, g)
	}
	other := gomutant.OracleGuidance{Symbol: "q.D", Suggestion: "different"}
	appendGuidance(&entries, other)
	if len(entries) != 2 || len(entries[0].Targets) != 3 || entries[0].Targets[2] != "p.C" || len(entries[1].Targets) != 1 {
		t.Fatalf("aggregated guidance = %+v", entries)
	}
}

// The run response's finding rows and per-finding survivors cap with
// the remainders counted (REQ-mcp-envelope).
func TestCapRunFindingsCountsTheRemainder(t *testing.T) {
	findings := make([]gomutant.Finding, 60)
	for i := range findings {
		survivors := make([]gomutant.Survivor, 25)
		for j := range survivors {
			survivors[j] = gomutant.Survivor{Position: "f.go:1:1", Operator: "op"}
		}
		findings[i] = gomutant.Finding{Symbol: "p.F", Survivors: survivors}
	}
	rows, omitted := capRunFindings(findings)
	if len(rows) != 50 || omitted != 10 {
		t.Fatalf("finding rows = %d, omitted %d", len(rows), omitted)
	}
	if len(rows[0].Open) != 20 || rows[0].OmittedOpen != 5 {
		t.Fatalf("open rows = %d, omitted %d", len(rows[0].Open), rows[0].OmittedOpen)
	}
}
