package gomutant

import (
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
)

// TestAttestationCarryNamesADerivationMove pins the carry report's
// one rendering: a carry whose prior record and current measurement
// were folded under different closure derivations names the move, so
// the reader knows the pins moved under a new derivation rather than a
// source edit; a carry under one derivation names nothing.
//
//gofresh:pure
func TestAttestationCarryNamesADerivationMove(t *testing.T) {
	held := derivationMove(SubjectEvidence{Fingerprint: gofresh.Fingerprint{ClosureStrategy: "a@1", ResultKind: gofresh.CodeResult}}, SubjectEvidence{Fingerprint: gofresh.Fingerprint{ClosureStrategy: "a@1", ResultKind: gofresh.CodeResult}})
	if held != "" {
		t.Fatalf("a held derivation named a move: %q", held)
	}
	if unknown := derivationMove(SubjectEvidence{}, SubjectEvidence{Fingerprint: gofresh.Fingerprint{ClosureStrategy: "a@2", ResultKind: gofresh.CodeResult}}); unknown != "" {
		t.Fatalf("a pre-field prior named a move: %q", unknown)
	}
	moved := derivationMove(SubjectEvidence{Fingerprint: gofresh.Fingerprint{ClosureStrategy: "a@1", ResultKind: gofresh.CodeResult}}, SubjectEvidence{Fingerprint: gofresh.Fingerprint{ClosureStrategy: "a@2", ResultKind: gofresh.CodeResult}})
	if moved != "a@1 -> a@2" {
		t.Fatalf("derivation move = %q, want the prior and current named", moved)
	}
	plain := AttestationCarry{Symbol: "p.F", Position: "f.go:1:1", Operator: "op"}.Text()
	if !strings.HasSuffix(plain, "the mutant survived re-execution") || strings.Contains(plain, "derivation") {
		t.Fatalf("a held-derivation carry renders %q", plain)
	}
	named := AttestationCarry{Symbol: "p.F", Position: "f.go:1:1", Operator: "op", Derivation: moved}.Text()
	if !strings.HasSuffix(named, " (closure derivation a@1 -> a@2)") || !strings.HasPrefix(named, plain) {
		t.Fatalf("a derivation-move carry renders %q, want the held rendering with the move appended", named)
	}
}
