package mcpserver

import (
	"context"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The run response's per-record layer is the layer the WRITE routed the
// record to, never the store's predicate re-run after the write: a
// write that routes a measured record machine-local under a reason of
// its own is what the row states, whatever the predicate would say
// (REQ-result-local-signpost, REQ-result-layers).
func TestToolRunRowsStateTheWritesRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := serverAt(t)
	const symbol = "example.com/fixture/lib.Weak"
	s.updateDocument = func(_ context.Context, _ string, change func([]gomutant.Finding) ([]gomutant.Finding, error)) (gomutant.Routing, error) {
		if _, err := change(nil); err != nil {
			return nil, err
		}
		return gomutant.Routing{symbol: {Layer: gomutant.LayerLocal, Reasons: []string{"the write's own reason"}}}, nil
	}
	_, out, err := s.toolRun(context.Background(), nil, runIn{
		TargetsJSON:      `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`,
		Budget:           1,
		OracleTimeoutSec: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one measured row", out.Findings)
	}
	row := out.Findings[0]
	if row.Symbol != symbol || row.Layer != gomutant.LayerLocal || row.LayerReason != "the write's own reason" {
		t.Fatalf("row = %s %s/%q, want the write's routing", row.Symbol, row.Layer, row.LayerReason)
	}
	if out.MachineLocalOnly != 1 {
		t.Fatalf("machine-local aggregate %d, want the routed record", out.MachineLocalOnly)
	}
}
