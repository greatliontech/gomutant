// Command gomutant is the CLI over the gomutant library: it discovers or
// loads targets, runs mutants, maintains the findings document, and
// dispositions survivors. Findings are advisory (REQ-result-findings):
// the exit code reports operational failure, never open findings — build
// policy over findings belongs to the caller's scripting.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/greatliontech/gomutant/internal/mcpserver"
	"github.com/spf13/cobra"
)

const defaultFindings = ".gomutant/findings.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := newRootCommand()
	cmd.SetArgs(normalizeLegacyFlags(args))
	return cmd.Execute()
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gomutant",
		Short:         "Mutation testing for Go",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("a command is required")
		},
	}
	cmd.AddCommand(newRunCommand(), newFindingsCommand(), newAttestCommand(), newEphemeralCommand(), newMCPCommand())
	return cmd
}

func normalizeLegacyFlags(args []string) []string {
	long := map[string]bool{
		"budget": true, "changed": true, "dir": true, "file": true, "findings": true,
		"force": true, "jobs": true, "label": true, "operator": true, "position": true,
		"reason": true, "replacement": true, "run": true, "symbol": true, "targets": true,
		"test-pkg": true, "timeout": true,
	}
	normalized := append([]string(nil), args...)
	for i := 0; i < len(normalized); i++ {
		arg := normalized[i]
		if arg == "--" {
			break
		}
		if len(arg) < 3 || arg[0] != '-' {
			continue
		}
		legacy := arg[1] != '-'
		name := strings.TrimLeft(arg, "-")
		before, _, hasValue := strings.Cut(name, "=")
		if hasValue {
			name = before
		}
		if long[name] {
			if legacy {
				normalized[i] = "-" + arg
			}
			if name != "force" && !hasValue {
				i++
			}
		}
	}
	return normalized
}

type runOptions struct {
	dir, changed, targetsFile, findingsFile string
	budget, jobs                            int
	timeout                                 time.Duration
	force                                   bool
}

func newRunCommand() *cobra.Command {
	o := runOptions{}
	cmd := &cobra.Command{Use: "run", Short: "Measure mutants and update findings", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return runCommand(o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.IntVar(&o.budget, "budget", 0, "mutants per symbol; 0 = exhaustive")
	f.DurationVar(&o.timeout, "timeout", 60*time.Second, "one mutant's oracle run budget")
	f.IntVar(&o.jobs, "jobs", 0, "concurrent mutant runs; 0 = half the CPUs")
	f.BoolVar(&o.force, "force", false, "re-measure targets whose prior finding still covers")
	f.StringVar(&o.changed, "changed", "", "target only symbols whose bodies differ from this git ref")
	f.StringVar(&o.targetsFile, "targets", "", "JSON targets document; overrides discovery")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read and update")
	return cmd
}

func runCommand(o runOptions) error {
	tree, err := gomutant.Load(o.dir)
	if err != nil {
		return err
	}

	var targets []gomutant.Target
	var residue []gomutant.Residue
	switch {
	case o.targetsFile != "":
		data, err := os.ReadFile(o.targetsFile)
		if err != nil {
			return err
		}
		if targets, err = gomutant.LoadTargets(data); err != nil {
			return err
		}
	case o.changed != "":
		paths, err := gitref.ChangedPaths(o.dir, o.changed)
		if err != nil {
			return err
		}
		targets, residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
			return gitref.Show(o.dir, o.changed, p)
		})
	default:
		targets = tree.Discover()
	}
	for _, r := range residue {
		fmt.Printf("changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	if len(targets) == 0 {
		fmt.Println("no targets")
		return nil
	}

	// The default document anchors at -dir, matching the MCP face: the two
	// faces compose through one record wherever the tree is.
	docPath := findingsAt(o.dir, o.findingsFile)
	prior, err := loadFindings(docPath)
	if err != nil {
		return err
	}
	findings, err := tree.Run(context.Background(), targets, gomutant.Options{
		Budget: o.budget, Timeout: o.timeout, Jobs: o.jobs, Force: o.force, Prior: prior,
	})
	if err != nil {
		return err
	}

	for _, f := range findings {
		switch {
		case f.Skipped != "":
			fmt.Printf("skipped   %s  (%s)\n", f.Symbol, f.Skipped)
		case f.Cached:
			fmt.Printf("cached    %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		default:
			fmt.Printf("measured  %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		}
		for _, s := range f.Open() {
			fmt.Printf("          survivor %s %s\n", s.Position, s.Operator)
		}
	}
	return gomutant.UpdateDocument(docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		return gomutant.MergeFindings(current, findings), nil
	})
}

type findingsOptions struct{ dir, findingsFile, label string }

func newFindingsCommand() *cobra.Command {
	o := findingsOptions{}
	cmd := &cobra.Command{Use: "findings", Short: "List open mutation findings", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return findingsCommand(o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read")
	f.StringVar(&o.label, "label", "", "show only findings carrying this label")
	return cmd
}

func findingsCommand(o findingsOptions) error {
	all, err := loadFindings(findingsAt(o.dir, o.findingsFile))
	if err != nil {
		return err
	}
	// Group open findings by label (REQ-target-labels: grouped and printed,
	// never interpreted); an unlabeled finding groups under "(unlabeled)".
	byLabel := map[string][]string{}
	for _, f := range all {
		open := f.Open()
		if len(open) == 0 {
			continue
		}
		labels := f.Labels
		if len(labels) == 0 {
			labels = []string{"(unlabeled)"}
		}
		for _, l := range labels {
			if o.label != "" && l != o.label {
				continue
			}
			for _, s := range open {
				byLabel[l] = append(byLabel[l], fmt.Sprintf("%s  %s %s", f.Symbol, s.Position, s.Operator))
			}
		}
	}
	if len(byLabel) == 0 {
		fmt.Println("no open findings")
		return nil
	}
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		fmt.Printf("%s\n", l)
		for _, line := range byLabel[l] {
			fmt.Printf("  %s\n", line)
		}
	}
	return nil
}

type attestOptions struct{ dir, findingsFile, symbol, position, operator, reason string }

func newAttestCommand() *cobra.Command {
	o := attestOptions{}
	cmd := &cobra.Command{Use: "attest", Short: "Attest an equivalent surviving mutant", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return attestCommand(o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to update")
	f.StringVar(&o.symbol, "symbol", "", "the mutated symbol")
	f.StringVar(&o.position, "position", "", "the survivor's position (file:line:col)")
	f.StringVar(&o.operator, "operator", "", "the survivor's operator")
	f.StringVar(&o.reason, "reason", "", "why the mutant is equivalent")
	return cmd
}

func attestCommand(o attestOptions) error {
	if o.symbol == "" || o.position == "" || o.operator == "" || o.reason == "" {
		return fmt.Errorf("attest needs --symbol, --position, --operator, and --reason")
	}
	return gomutant.UpdateDocument(findingsAt(o.dir, o.findingsFile), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == o.symbol {
				return all, all[i].Attest(o.position, o.operator, o.reason)
			}
		}
		return nil, fmt.Errorf("no finding for %s", o.symbol)
	})
}

func newMCPCommand() *cobra.Command {
	dir := "."
	cmd := &cobra.Command{Use: "mcp", Short: "Serve gomutant over MCP", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return mcpserver.New(dir).Run(context.Background())
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "tree root (module or workspace)")
	return cmd
}

type ephemeralOptions struct {
	dir, file, replacement, testPkg, runPat string
	timeout                                 time.Duration
}

func newEphemeralCommand() *cobra.Command {
	o := ephemeralOptions{}
	cmd := &cobra.Command{Use: "ephemeral", Short: "Run one replacement source as a manual mutant", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return ephemeralCommand(o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.file, "file", "", "tree-relative source file to replace")
	f.StringVar(&o.replacement, "replacement", "", "path to the whole replacement source")
	f.StringVar(&o.testPkg, "test-pkg", "", "package whose named test decides the kill")
	f.StringVar(&o.runPat, "run", "", "-run pattern naming the deciding test")
	f.DurationVar(&o.timeout, "timeout", 60*time.Second, "the run's budget")
	return cmd
}

func ephemeralCommand(o ephemeralOptions) error {
	if o.file == "" || o.replacement == "" || o.testPkg == "" || o.runPat == "" {
		return fmt.Errorf("ephemeral needs --file, --replacement, --test-pkg, and --run")
	}
	mutant, err := os.ReadFile(o.replacement)
	if err != nil {
		return err
	}
	tree, err := gomutant.Load(o.dir)
	if err != nil {
		return err
	}
	res, err := tree.Ephemeral(context.Background(), o.file, mutant, o.testPkg, o.runPat, o.timeout)
	if err != nil {
		return err
	}
	if res.Killed {
		fmt.Printf("killed    %s  by %s\n", res.File, res.Killer)
	} else {
		fmt.Printf("SURVIVED  %s  — %s did not notice the mutation\n", res.File, res.Run)
	}
	return nil
}

// loadFindings reads the findings document; a missing file is an empty set.
func loadFindings(path string) ([]gomutant.Finding, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return gomutant.ParseFindings(data)
}

// findingsAt anchors a relative findings path at the tree root, matching the
// MCP face, so the two faces compose through one record (REQ-mcp-findings-doc).
func findingsAt(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, filepath.FromSlash(path))
}
