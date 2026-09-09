package mcpserver

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// A run commits every target and its final merge through the store its
// preparation opened: the exemptions record and the document caches the
// run was prepared with carry to the end. The record is torn once the
// campaign lock appears (preparation has opened the store by then); a
// commit through a fresh store would refuse on the torn record, the
// prepared store carries on.
func TestToolRunCommitsThroughThePreparedStore(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := serverAt(t)
	doc := gomutant.FindingsPathAt(s.dir, "")
	torn := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(doc + ".campaign"); err == nil {
				torn <- os.WriteFile(gomutant.ExemptionsPathFor(doc), []byte("{ torn"), 0o644)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		torn <- os.ErrDeadlineExceeded
	}()
	in := runIn{
		TargetsJSON:      `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`,
		Budget:           1,
		OracleTimeoutSec: 120,
	}
	_, out, err := s.toolRun(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("run with the exemptions record torn mid-campaign: %v; want the prepared store to carry every commit", err)
	}
	if err := <-torn; err != nil {
		t.Fatalf("tearing the record: %v", err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v; want the one measured target", out.Findings)
	}
	all, err := s.loadFindings("")
	if err == nil || len(all) != 0 {
		t.Fatalf("a fresh store over the torn record: %v, %d records; want the record's refusal (proof the tear landed before the commits)", err, len(all))
	}
}
