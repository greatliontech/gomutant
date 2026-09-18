package cmd

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// observeStretches installs the stretch observer for the test and
// returns a reader of the labels recorded so far, in order.
func observeStretches(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var labels []string
	seams.stretchObserver = func(label string) {
		mu.Lock()
		defer mu.Unlock()
		labels = append(labels, label)
	}
	t.Cleanup(func() { seams.stretchObserver = nil })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), labels...)
	}
}

// wantLoadingLineAheadOfTheRows fails unless the loading line is
// present with nothing but progress lines before it (a fast cadence
// may tick before the load's event) and no progress line from the
// first row on — the first line after the loading line that is
// neither a prepare line nor the cadence's.
func wantLoadingLineAheadOfTheRows(t *testing.T, output string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	loading, row := -1, -1
	for i, line := range lines {
		if line == "prepare   loading" && loading < 0 {
			loading = i
			continue
		}
		if loading >= 0 && row < 0 && !strings.HasPrefix(line, "prepare   ") && !strings.HasPrefix(line, "progress  ") {
			row = i
		}
	}
	if loading < 0 || row < 0 {
		t.Fatalf("output = %q; want the loading line ahead of a first row", output)
	}
	for _, line := range lines[:loading] {
		if !strings.HasPrefix(line, "progress  ") {
			t.Fatalf("a line other than the cadence's precedes the loading line: %q", output)
		}
	}
	for _, line := range lines[row:] {
		if strings.HasPrefix(line, "progress  ") {
			t.Fatalf("a progress line follows the first row: %q", output)
		}
	}
}

// slowWriter holds every write open for a while, so a cadence still
// running while the rows render lands its ticks between them: an
// epilogue that never joined the cadence is caught, not raced.
type slowWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *slowWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	time.Sleep(3 * time.Millisecond)
	return w.buf.Write(p)
}

func (w *slowWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

var _ io.Writer = (*slowWriter)(nil)

// wantStretchesInOrder fails unless every wanted stretch heads a
// recorded label, in the order given; other stretches may interleave.
func wantStretchesInOrder(t *testing.T, labels, wants []string) {
	t.Helper()
	at := 0
	for _, label := range labels {
		if at < len(wants) && strings.HasPrefix(label, wants[at]) {
			at++
		}
	}
	if at != len(wants) {
		t.Fatalf("the stretches never named %q in order; recorded %q", wants[at], labels)
	}
}
