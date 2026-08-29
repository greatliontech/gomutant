package cmd

import (
	"bytes"
	"strings"
	"testing"

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
	registered := map[string][]string{}
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
			var flags []string
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return
				}
				flags = append(flags, f.Name)
			})
			registered[name] = flags
		}
	}
	walk("", newRootCommand())
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
			help, err := doc.Help("cli", name)
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
