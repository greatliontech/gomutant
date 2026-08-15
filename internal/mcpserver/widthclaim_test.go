package mcpserver

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The process-wide oracle-parallelism width has one owner: the first
// in-flight campaign's job count. A concurrent run with a matching
// count shares the claim; a differing count refuses naming the owner
// (REQ-exec-oracle-parallelism via the MCP surface clause).
func TestRunWidthClaimRefusesDifferingJobs(t *testing.T) {
	s := &Server{}
	if err := s.claimRunWidth(4); err != nil {
		t.Fatal(err)
	}
	if err := s.claimRunWidth(2); err == nil || !strings.Contains(err.Error(), "jobs=4") || !strings.Contains(err.Error(), "jobs=2") {
		t.Fatalf("differing width claim = %v, want a refusal naming both counts", err)
	}
	if err := s.claimRunWidth(4); err != nil {
		t.Fatalf("matching width claim refused: %v", err)
	}
	s.releaseRunWidth()
	s.releaseRunWidth()
	if err := s.claimRunWidth(2); err != nil {
		t.Fatalf("width claim after full release refused: %v", err)
	}
	s.releaseRunWidth()
}

// jobs=0 resolves to the same width the run itself derives, so two
// spellings of one effective width share a claim and the refusal names
// a requestable count.
func TestRunWidthClaimResolvesDefaultJobs(t *testing.T) {
	s := &Server{}
	if err := s.claimRunWidth(0); err != nil {
		t.Fatal(err)
	}
	defer s.releaseRunWidth()
	if err := s.claimRunWidth(max(1, runtime.NumCPU()/2)); err != nil {
		t.Fatalf("explicit spelling of the default width refused: %v", err)
	}
	s.releaseRunWidth()
}

// An ephemeral probe's process-wide override and a campaign exclude
// each other under one guard - no check-then-install window.
func TestProbeOverrideAndCampaignExcludeEachOther(t *testing.T) {
	s := &Server{}
	if err := s.claimProbeOverride(); err != nil {
		t.Fatal(err)
	}
	if err := s.claimRunWidth(4); err == nil || !strings.Contains(err.Error(), "ephemeral probe") {
		t.Fatalf("campaign admitted during a probe override: %v", err)
	}
	s.releaseProbeOverride()
	if err := s.claimRunWidth(4); err != nil {
		t.Fatal(err)
	}
	if err := s.claimProbeOverride(); err == nil || !strings.Contains(err.Error(), "run in flight") {
		t.Fatalf("probe override admitted during a campaign: %v", err)
	}
	s.releaseRunWidth()
}

// A drift refusal folds its attestation sheds into the error text - the
// SDK renders only the error on failure and the document already
// stripped them, so the text is the sheds' one surfacing
// (REQ-attest-survivor).
func TestDriftErrorCarriesSheds(t *testing.T) {
	base := fmt.Errorf("tree drifted")
	if err := driftError(base, nil); err != base {
		t.Fatalf("shed-free drift rewrapped: %v", err)
	}
	err := driftError(base, []string{"p.F f.go:1:1 zero-return: killed by TestF"})
	if err == nil || !strings.Contains(err.Error(), "tree drifted") || !strings.Contains(err.Error(), "re-attest if genuinely equivalent") || !strings.Contains(err.Error(), "killed by TestF") {
		t.Fatalf("drift error missing sheds: %v", err)
	}
}

// withHeartbeat emits still-working notifications on the caller's ONE
// notifier while the stretch runs - and stays silent without one
// (REQ-mcp-envelope's no-silent-stretch clause for tree loads and
// oracle stretches).
func TestWithHeartbeatNotifiesDuringTheStretch(t *testing.T) {
	prior := heartbeatInterval
	heartbeatInterval = 5 * time.Millisecond
	defer func() { heartbeatInterval = prior }()
	var beats atomic.Int64
	notify := func(string) { beats.Add(1) }
	got, err := withHeartbeat(context.Background(), notify, "probe", func(context.Context) (int, error) {
		time.Sleep(60 * time.Millisecond)
		return 7, nil
	})
	if err != nil || got != 7 {
		t.Fatalf("withHeartbeat = %d, %v", got, err)
	}
	if beats.Load() == 0 {
		t.Fatal("no still-working notification during a slow stretch")
	}
	if _, err := withHeartbeat(context.Background(), nil, "probe", func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("nil-notifier stretch failed: %v", err)
	}
}
