package cmd

import (
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/spectable"
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

// The CLI edit-batch shape is stated where its readers look — the
// batch flag's help and the guidance document's knob — and both state
// the same wrapper the parser accepts.
func TestBatchShapeIsStatedWhereItIsRead(t *testing.T) {
	const shape = `{"edits":[`
	usage := newEphemeralCommand().Flags().Lookup("batch").Usage
	if !strings.Contains(usage, shape) {
		t.Fatalf("--batch help %q does not state the file shape", usage)
	}
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Verbs {
		if v.Name != "ephemeral" {
			continue
		}
		knobs := map[string]string{}
		for _, k := range v.Knobs {
			knobs[k.Name] = k.Text
		}
		// The field names each knob states are the types' own wire names.
		for knob, typ := range map[string]reflect.Type{"batch_edits": reflect.TypeOf(gomutant.BatchEdit{}), "edits": reflect.TypeOf(gomutant.Edit{})} {
			text, ok := knobs[knob]
			if !ok {
				t.Fatalf("no %s knob in the guidance document", knob)
			}
			for i := 0; i < typ.NumField(); i++ {
				name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
				if !strings.Contains(text, name) {
					t.Fatalf("the %s knob %q does not name the field %q", knob, text, name)
				}
			}
		}
		if !strings.Contains(knobs["batch_edits"], shape) {
			t.Fatalf("the batch_edits knob %q does not state the file shape", knobs["batch_edits"])
		}
		if _, err := gomutant.ParseEditBatch([]byte(`{"edits":[{"file":"a.go","old_string":"a","new_string":"b"}]}`)); err != nil {
			t.Fatalf("the stated shape does not parse: %v", err)
		}
		return
	}
	t.Fatal("no ephemeral verb in the guidance document")
}

// The CLI face keys the bounds its own surface-table column states —
// the rosters the human summary cuts — to the policy that holds them,
// failing closed on any bound no phrase keys (REQ-mcp-surfaces).
func TestSurfaceTableStatesTheCLIBounds(t *testing.T) {
	table := spectable.Section(t, filepath.Join("..", "..", "docs", "specs", "mcp.md"), "REQ-mcp-surfaces")
	column := spectable.Columns(t, table, 3)
	rosters := regexp.MustCompile(`unreached roster \(at (\d+) each\)`)
	seen := map[int]bool{}
	for _, m := range rosters.FindAllStringSubmatchIndex(column, -1) {
		n, _ := strconv.Atoi(column[m[2]:m[3]])
		if n != unreachedShown {
			t.Fatalf("the CLI column states rosters at %d; the unreached roster is cut at %d", n, unreachedShown)
		}
		if n != gomutant.PostureCap {
			t.Fatalf("the CLI column states rosters at %d; the not-reusable roster is cut at %d", n, gomutant.PostureCap)
		}
		seen[m[2]] = true
	}
	if len(seen) == 0 {
		t.Fatal("the CLI column states no roster bound")
	}
	for _, m := range regexp.MustCompile(`\b(?:at|to) (\d+)\b`).FindAllStringSubmatchIndex(column, -1) {
		if !seen[m[2]] {
			t.Fatalf("the CLI column states a bound %q that no phrase keys to a policy field", column[m[0]:m[1]])
		}
	}
	// The CLI's edit-batch shape is stated in its column.
	if !strings.Contains(column, `{"edits":[`) {
		t.Fatal("the surface table's CLI column does not state the edit-batch shape")
	}
}
