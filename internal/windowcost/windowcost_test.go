package windowcost

import "testing"

// Membership constants pin the observable partition independently of pricing
// and confirmation: removing an execution optimization cannot move windows.
func TestWindowMembershipConstants(t *testing.T) {
	if CandidatesPerWorker != 8 || CandidatesFloor != 64 || CandidatesMin != 8 || ExecutionBudget != 512 {
		t.Fatalf("the window's constants moved: %d %d %d %d", CandidatesPerWorker, CandidatesFloor, CandidatesMin, ExecutionBudget)
	}
}

// Serial kill confirmation retains its reproduction streak and sample stride.
func TestConfirmationConstants(t *testing.T) {
	if ConfirmStreak != 3 || ConfirmStride != 4 {
		t.Fatalf("the confirmation constants moved: %d %d", ConfirmStreak, ConfirmStride)
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
