package cmd

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guidance"
	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The CLI surface and the guidance document cannot drift: every
// visible leaf command's spelling and every local flag is documented,
// in both directions, and served Short/Long ARE the document's
// projections (REQ-mcp-guidance). Cobra's help/completion plumbing is
// surface plumbing, not verbs.
func TestGuidanceCoversTheCLISurface(t *testing.T) {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]guidance.Registered{}
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, child := range c.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			flags := guidance.Registered{}
			child.Flags().VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return
				}
				// The registration carries whether cobra prints a default for
				// the flag — the fact the coverage judgment scopes its
				// default-spelling rule by.
				flags[f.Name] = !zeroDefault(f)
				// Every usage string is the document's rendering — the
				// knob's terse clause in pflag's usage grammar (no
				// code span, since pflag reads the first back-quoted
				// word as the value name; no default parenthetical,
				// since cobra prints the default) — never a second
				// literal.
				k, err := doc.Knob("cli", name, f.Name)
				if err != nil {
					t.Errorf("%s --%s: %v", name, f.Name, err)
					return
				}
				want := strings.ReplaceAll(k.Clause(), "`", "")
				if i := strings.Index(want, " (default "); i >= 0 {
					if j := strings.Index(want[i:], ")"); j >= 0 {
						want = want[:i] + want[i+j+1:]
					}
				}
				if f.Usage != want {
					t.Errorf("%s --%s usage diverged from the document's rendering:\ncli %q\ndoc %q", name, f.Name, f.Usage, want)
				}
				if strings.Contains(f.Usage, "`") || strings.Contains(f.Usage, "(default ") {
					t.Errorf("%s --%s usage carries a code span or a default the face prints itself: %q", name, f.Name, f.Usage)
				}
				// cobra appends the default of a non-zero flag, so such
				// a usage never spells one itself, in any wording. Zero
				// is pflag's per-type notion; a type this table does not
				// know is checked, never skipped.
				if !zeroDefault(f) && strings.Contains(strings.ToLower(f.Usage), "default") {
					t.Errorf("%s --%s usage spells a default cobra prints (%s %q): %q", name, f.Name, f.Value.Type(), f.DefValue, f.Usage)
				}
			})
			registered[name] = flags
		}
	}
	walk("", newRootCommand())
	// The registration's fact pinned by two literals — a flag whose
	// default cobra prints and one whose default it does not — so the
	// judgment's default-spelling rule keeps its population once the
	// inline lint above goes.
	if !registered["run"]["findings"] || registered["run"]["budget"] {
		t.Fatalf("run registration = %v; want findings non-zero and budget zero", registered["run"])
	}
	// One rendering pinned by its literal, so the comparison above is
	// never the projection judging itself.
	if run, _, err := newRootCommand().Find([]string{"run"}); err != nil {
		t.Fatal(err)
	} else if budget := run.Flags().Lookup("budget"); budget == nil || budget.Usage != "candidates per symbol (0 means exhaustive)" {
		t.Fatalf("run --budget usage = %+v, want the document's terse clause \"candidates per symbol (0 means exhaustive)\"", budget)
	}
	// The grammar pinned by its literals: the `attest` span's quotes
	// and the false default gone from a boolean's usage, the findings
	// default gone from a string's, and the rendered help printing
	// --reattest with no value name and the findings default once.
	ephemeral := newEphemeralCommand()
	if got := ephemeral.Flags().Lookup("reattest").Usage; got != "with attest: replace an existing attestation of the same mutant instead of refusing" {
		t.Fatalf("ephemeral --reattest usage = %q", got)
	}
	if got := newPruneCommand().Flags().Lookup("findings").Usage; got != "findings document path" {
		t.Fatalf("prune --findings usage = %q", got)
	}
	help := ephemeral.UsageString()
	if !regexp.MustCompile(`--reattest\s+with attest:`).MatchString(help) {
		t.Fatalf("ephemeral help prints --reattest with a value name:\n%s", help)
	}
	if n := strings.Count(help, "(default \".gomutant/findings.json\")"); n != 1 || strings.Contains(help, "(default .gomutant") {
		t.Fatalf("ephemeral help prints the findings default %d times:\n%s", n, help)
	}
	// The long help names the served path to the knobs' whole prose.
	if long := newRunCommand().Long; !strings.HasSuffix(long, "\n\nThe knobs' whole prose: gomutant guidance run.") {
		t.Fatalf("run Long lacks the guidance pointer: %q", long)
	}
	if long := newVersionCommand().Long; strings.Contains(long, "whole prose") {
		t.Fatalf("version Long points at knob prose it has none of: %q", long)
	}
	// The runs default is cobra's alone in the rendered help.
	if n := strings.Count(strings.ToLower(help), "default 1"); n != 1 {
		t.Fatalf("ephemeral help spells the runs default %d times:\n%s", n, help)
	}
	defects, err := doc.Coverage("cli", registered)
	if err != nil || len(defects) != 0 {
		t.Fatalf("cli coverage: err=%v defects:\n%s", err, strings.Join(defects, "\n"))
	}
	root := newRootCommand()
	for name := range registered {
		c, _, err := root.Find(strings.Fields(name))
		if err != nil {
			t.Fatalf("find %q: %v", name, err)
		}
		short, err := doc.Description("cli", name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if c.Short != short {
			t.Errorf("%q Short diverged:\ncli %q\ndoc %q", name, c.Short, short)
		}
		if c.Long != "" {
			// The cobra Long is the knobless help rendering — cobra's
			// own Flags: block carries the knob list on this surface.
			// A verb with cli knobs points at the guidance command for
			// their whole prose; a knobless verb (version, guidance)
			// points nowhere.
			help, err := doc.Help("cli", name)
			if err == nil && c.Flags().HasFlags() {
				help += "\n\n" + knobProsePointer(name)
			}
			if err != nil || c.Long != help {
				t.Errorf("%q Long diverged from Help (err=%v):\ncli %q\ndoc %q", name, err, c.Long, help)
			}
			if strings.Contains(c.Long, "\nknobs:") {
				t.Errorf("%q Long carries the knobs block beside cobra's Flags", name)
			}
		}
	}
}

// The guidance command serves the document under cli spellings: a
// verb's long section, the decision map for no verb, and a teaching
// error for an unknown one (REQ-mcp-guidance).
func TestGuidanceCommandServesTheDocument(t *testing.T) {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		root := newRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	got, err := run("guidance", "run")
	if err != nil {
		t.Fatal(err)
	}
	long, _ := doc.Long("cli", "run")
	if strings.TrimSuffix(got, "\n") != long {
		t.Fatalf("guidance run diverged:\n%q\nwant\n%q", got, long)
	}
	got, err = run("guidance")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(got, "\n") != doc.Orientation() {
		t.Fatalf("guidance orientation diverged: %q", got)
	}
	if _, err = run("guidance", "vanished"); err == nil || !strings.Contains(err.Error(), "decision map") {
		t.Fatalf("unknown verb: err = %v", err)
	}
}

// A flag the document does not carry refuses the command's
// construction: the served set cannot outgrow the document silently
// (REQ-mcp-guidance).
func TestKnobbedFlagsRefusesAnUndocumentedFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().Bool("nonesuch", false, "")
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "nonesuch") {
			t.Fatalf("knobbedFlags over an undocumented flag: recovered %v, want a refusal naming nonesuch", r)
		}
	}()
	knobbedFlags(cmd, "run")
}

// zeroDefault is pflag's own per-type zero: the defaults cobra prints
// nothing for. A string is zero only when empty (a "0" or "false"
// string prints), and an unknown type answers false so its usage is
// checked.
func zeroDefault(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "string":
		return f.DefValue == ""
	case "bool":
		return f.DefValue == "false"
	case "int", "int64":
		return f.DefValue == "0"
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "stringArray", "stringSlice":
		return f.DefValue == "[]"
	}
	return false
}
