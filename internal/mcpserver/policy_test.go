package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The heartbeat paces on the one cadence every face's progress keeps
// (the MCP heartbeat clause of REQ-mcp-envelope), and the envelope's
// bounds are the ones the requirement names.
func TestHeartbeatAndEnvelopeAreOnePolicyEach(t *testing.T) {
	if heartbeatInterval != gomutant.ProgressCadence {
		t.Fatalf("heartbeat cadence = %s; want the shared %s", heartbeatInterval, gomutant.ProgressCadence)
	}
	if envelope.rows != 50 {
		t.Fatalf("envelope rows = %d; want the requirement's 50", envelope.rows)
	}
	if envelope.open != 20 {
		t.Fatalf("envelope open survivors = %d; want the requirement's 20", envelope.open)
	}
	if envelope.nested <= 0 || envelope.nested > envelope.rows {
		t.Fatalf("envelope nested = %d; want a bound tighter than the %d rows", envelope.nested, envelope.rows)
	}
	if envelope.reasons <= 0 {
		t.Fatalf("envelope reasons = %d; want a positive bound", envelope.reasons)
	}
	if envelope.streamed < envelope.rows {
		t.Fatalf("envelope streamed = %d; want at least the %d rows", envelope.streamed, envelope.rows)
	}
}

// The findings response's ephemeral-attestation list is a response row
// list: it caps at the row bound with the remainder counted
// (REQ-mcp-envelope).
func TestFindingsEphemeralAttestationsCapAtTheRowBound(t *testing.T) {
	s := serverAt(t)
	path := gomutant.EphemeralAttestationsPathFor(s.findingsPath(""))
	for i := 0; i < envelope.rows+3; i++ {
		att := gomutant.EphemeralAttestation{EditDigest: fmt.Sprintf("d%d", i), Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", Reason: "equivalent", Dirty: true}
		if err := gomutant.RecordEphemeralAttestation(context.Background(), path, att); err != nil {
			t.Fatal(err)
		}
	}
	_, out, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.EphemeralAttestations) != envelope.rows || out.OmittedEphemeralAttestations != 3 {
		t.Fatalf("ephemeral attestations = %d listed, %d omitted; want the %d row bound and 3 counted", len(out.EphemeralAttestations), out.OmittedEphemeralAttestations, envelope.rows)
	}
}
