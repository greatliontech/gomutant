package integrationtest

import (
	"os"
	"testing"
)

// The suite's own TestMain takes the selection's default, as every
// gate-carrying suite in the repository does.
func TestMain(m *testing.M) {
	DefaultToShort()
	os.Exit(m.Run())
}

// Under the default selection the binary runs -short unless the command
// line said otherwise; under the integration selection the command line
// alone decides. Pinned over the flag the testing package reads, so the
// pin holds in either selection and under either flag.
func TestDefaultSelectionRunsShortUnlessTold(t *testing.T) {
	Pin(t)
}

// The pin's judgment over every cell: the command line's word stands in
// both selections; without it the default selection must be short and
// the integration selection must not.
func TestVerdictOverEveryCell(t *testing.T) {
	for _, c := range []struct {
		explicit, enabled, short bool
		want                     string
	}{
		{false, false, true, ""},
		{false, false, false, "default selection: the suite is not running -short; its TestMain does not call integrationtest.DefaultToShort"},
		{false, true, false, ""},
		{false, true, true, "integration selection: -short set without the command line"},
		{true, false, false, ""},
		{true, false, true, ""},
		{true, true, false, ""},
		{true, true, true, ""},
	} {
		if got := Verdict(c.explicit, c.enabled, c.short); got != c.want {
			t.Fatalf("Verdict(%v, %v, %v) = %q, want %q", c.explicit, c.enabled, c.short, got, c.want)
		}
	}
}

// The command line's -test.short is recognised in the flag package's
// spellings and nowhere else: a positional token or another flag is
// not the word.
func TestExplicitShortReadsTheFlagsSpellings(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-test.short"}, true},
		{[]string{"--test.short"}, true},
		{[]string{"-test.short=false"}, true},
		{[]string{"-test.v=test2json", "--test.short=true", "-test.run", "X"}, true},
		{[]string{"test.short"}, false},
		{[]string{"-test.shortx"}, false},
		{[]string{"-test.run", "TestShort"}, false},
		{nil, false},
	} {
		if got := ExplicitShort(c.args); got != c.want {
			t.Fatalf("ExplicitShort(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
