package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

// Every verb's structured face answers to one flag name, `--json`, the
// verb saying the shape (a document for the one-shot verbs, JSON lines
// for the run stream); the former stream-specific spelling is refused
// (REQ-exec-run-status).
func TestStructuredOutputIsOneFlagName(t *testing.T) {
	root := newRootCommand()
	structured := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Flags().Lookup("jsonl") != nil {
				t.Fatalf("%s: a stream-specific structured flag beside the one name", sub.Name())
			}
			if sub.Flags().Lookup("json") != nil {
				structured[sub.Name()] = true
			}
			walk(sub)
		}
	}
	if root.Flags().Lookup("jsonl") != nil || root.PersistentFlags().Lookup("jsonl") != nil {
		t.Fatal("the root carries a stream-specific structured flag")
	}
	walk(root)
	for _, verb := range []string{"run", "findings", "discover"} {
		if !structured[verb] {
			t.Fatalf("%s: no --json flag", verb)
		}
	}
	run := newRunCommand()
	if err := run.Flags().Parse([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := run.Flags().GetBool("json"); !v {
		t.Fatal("run --json did not select the structured face")
	}
	if err := newRunCommand().Flags().Parse([]string{"--jsonl"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("run --jsonl = %v; want the unknown-flag refusal", err)
	}
}

// The progress cadence is one policy: the run and ephemeral flags
// default to the library's cadence, and the verb line keeps it
// (REQ-exec-run-status).
func TestProgressCadenceIsOnePolicy(t *testing.T) {
	if gomutant.ProgressCadence != 30*time.Second {
		t.Fatalf("cadence = %s; want the thirty seconds the spec fixes", gomutant.ProgressCadence)
	}
	want := gomutant.ProgressCadence.String()
	for _, verb := range []string{"run", "ephemeral"} {
		cmd := newRootCommand()
		sub, _, err := cmd.Find([]string{verb})
		if err != nil {
			t.Fatal(err)
		}
		f := sub.Flags().Lookup("progress-interval")
		if f == nil || f.DefValue != want {
			t.Fatalf("%s --progress-interval default = %v; want %s", verb, f, want)
		}
	}
	if verbProgressInterval != gomutant.ProgressCadence {
		t.Fatalf("verb line cadence = %s; want %s", verbProgressInterval, gomutant.ProgressCadence)
	}
}
