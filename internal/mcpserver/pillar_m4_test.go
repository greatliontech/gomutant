package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
)

func TestPillarM4RenderBoundExpiryPreservesSummaryAndOutcomeOnResponse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":          "module example.com/current\n\ngo 1.24\n",
		"current.go":      "package current\n\nfunc Value() int { return 1 }\n",
		"current_test.go": "package current\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{
			Symbol: symbol,
			Fingerprint: gofresh.Fingerprint{
				MaximalClosure:       "closure",
				TestVariantClosure:   "tv",
				ObservationAssertion: "caller assertion",
				RuntimeInputs:        "manifest",
				RuntimeDigest:        "digest",
				Guards:               guard.Guards{Toolchain: "go", BuildConfig: "build"},
				ObservationProof:     gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: symbol}, Observable: true, Evidence: "proof"},
				ResultKind:           gofresh.CodeResult,
			},
		}
	}
	path := gomutant.FindingsPathAt(dir, "")
	if err := gomutant.UpdateDocument(context.Background(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{{
			Symbol:         "example.com/current.Old",
			BodyHash:       "body",
			OperatorSet:    "go/2",
			OracleTimeout:  "1m0s",
			Dirty:          true,
			TargetEvidence: evidence("example.com/current.Old"),
			OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/current.TestOld")},
		}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	bound := seams.postCommitRenderBound
	seams.postCommitRenderBound = time.Nanosecond
	t.Cleanup(func() { seams.postCommitRenderBound = bound })
	_, out, err := New(dir).toolRun(context.Background(), nil, runIn{Budget: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("toolRun error = %v, want context.DeadlineExceeded", err)
	}
	if out.Summary.Targets != 1 || out.Document == "" || out.Note == "" {
		t.Fatalf("render-bound response discarded summary/document/note: %+v", out)
	}
}
