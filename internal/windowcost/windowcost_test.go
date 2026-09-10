package windowcost

import (
	"testing"
	"time"
)

// The constants are contract (the partition is observable across runs),
// and the one cross-model invariant holds: the candidate minimum is the
// audit share divisor it exists to honour, so a window at the minimum
// spends exactly one sample's share on the floored audit.
func TestConstantsAreContractAndTied(t *testing.T) {
	if CandidatesPerWorker != 8 || CandidatesFloor != 64 || CandidatesMin != 8 || ExecutionBudget != 512 {
		t.Fatalf("the window's constants moved: %d %d %d %d", CandidatesPerWorker, CandidatesFloor, CandidatesMin, ExecutionBudget)
	}
	if CandidatesMin != AuditShareDivisor {
		t.Fatalf("the candidate minimum %d is not the audit share divisor %d it exists to honour", CandidatesMin, AuditShareDivisor)
	}
	if AuditNarrowedCap != 4 || ConfirmStreak != 3 || ConfirmStride != 4 {
		t.Fatalf("the audit ceiling or confirmation constants moved: %d %d %d", AuditNarrowedCap, ConfirmStreak, ConfirmStride)
	}
}

// The bounds derive from the worker count alone; a fixed window takes
// both, so the execution budget can never split it.
func TestBoundsDeriveFromTheWorkerCount(t *testing.T) {
	for _, tc := range []struct{ jobs, ceiling, minimum int }{{1, 64, 8}, {4, 64, 8}, {8, 64, 8}, {9, 72, 9}, {16, 128, 16}} {
		if c, m := Bounds(tc.jobs, 0); c != tc.ceiling || m != tc.minimum {
			t.Fatalf("Bounds(%d) = %d, %d; want %d, %d", tc.jobs, c, m, tc.ceiling, tc.minimum)
		}
		if c, m := Bounds(tc.jobs, 0); c < m {
			t.Fatalf("Bounds(%d): ceiling %d below minimum %d", tc.jobs, c, m)
		}
	}
	if c, m := Bounds(16, 5); c != 5 || m != 5 {
		t.Fatalf("Bounds(16, 5) = %d, %d; want 5, 5", c, m)
	}
}

// The membership bound is the product and nothing else: no duration and
// no document state can enter a function of two integers.
func TestExecutionsIsTheProduct(t *testing.T) {
	if Executions(3, 7) != 21 || Executions(0, 7) != 0 || Executions(3, 0) != 0 {
		t.Fatal("Executions is not candidates × oracle tests")
	}
}

// The derived cap: floored at one, savings/(divisor × unit) between,
// the ceiling above; an unpriced unit derives the floor.
func TestDerivedAuditCapShareFloorAndCeiling(t *testing.T) {
	unit := time.Minute
	if got := DerivedAuditCap(0, unit); got != 1 {
		t.Fatalf("no savings: cap = %d, want the floor 1", got)
	}
	if got := DerivedAuditCap(AuditShareDivisor*2*unit, unit); got != 2 {
		t.Fatalf("cap = %d, want savings/(%d×unit) = 2", got, AuditShareDivisor)
	}
	if got := DerivedAuditCap(AuditShareDivisor*100*unit, unit); got != AuditNarrowedCap {
		t.Fatalf("cap = %d, want the ceiling %d", got, AuditNarrowedCap)
	}
	if got := DerivedAuditCap(time.Hour, 0); got != 1 {
		t.Fatalf("unpriced unit: cap = %d, want the floor 1", got)
	}
}
