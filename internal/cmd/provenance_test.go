package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// Attest guards provenance BEFORE its write (REQ-exec-provenance): a
// skewed binary refuses outright — the findings document is not
// mutated and no success echoes.
func TestAttestRefusesToolchainSkewBeforeWriting(t *testing.T) {
	restore := engine.SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return "go99.1.0", nil
	})
	t.Cleanup(restore)
	dir := t.TempDir()
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = attestCommand(context.Background(), attestOptions{
		dir: dir, findingsFile: docPath,
		symbol: "p.F", position: "f.go:1:1", operator: "op", reason: "equivalent",
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "toolchain provenance") {
		t.Fatalf("attest under skew = %v, want the provenance refusal", err)
	}
	if out.String() != "" {
		t.Fatalf("attest under skew echoed %q before refusing", out.String())
	}
	got, err := os.ReadFile(docPath)
	if err != nil || !bytes.Equal(got, document) {
		t.Fatalf("attest under skew mutated the findings document: %v", err)
	}
}
