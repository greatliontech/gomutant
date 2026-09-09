package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/gitfixture"
)

// The final replacement is the success boundary on this face too: an
// interrupt or deadline after it never fails a committed changed-ref
// run — the command succeeds with its rows and the store holds them
// (REQ-exec-cancellation).
func TestRunDeadlineAfterTheFinalReplacementStillSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	dir := gitfixture.Changed(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	afterFinalReplacementForTest = cancel
	t.Cleanup(func() { afterFinalReplacementForTest = nil })
	var out bytes.Buffer
	if err := runCommand(ctx, runOptions{dir: dir, changed: "HEAD", findingsFile: defaultFindings, output: &out}); err != nil {
		t.Fatalf("a cancellation after the final replacement failed the run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "example.com/dl.Value") || !strings.Contains(out.String(), "on the delta") {
		t.Fatalf("post-boundary output lacks the row and its cut:\n%s", out.String())
	}
}
