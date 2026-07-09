// Command gomutant is the CLI over the gomutant library: it discovers or
// loads targets, runs mutants, maintains the findings document, and
// dispositions survivors. Findings are advisory (REQ-result-findings):
// the exit code reports operational failure, never open findings — build
// policy over findings belongs to the caller's scripting.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/greatliontech/gomutant/internal/mcpserver"
)

const defaultFindings = ".gomutant/findings.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gomutant <run|findings|attest|ephemeral|mcp> [flags]")
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "findings":
		return cmdFindings(args[1:])
	case "attest":
		return cmdAttest(args[1:])
	case "ephemeral":
		return cmdEphemeral(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	default:
		return fmt.Errorf("unknown command %q (want run, findings, attest, or ephemeral)", args[0])
	}
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", ".", "tree root (module or workspace)")
	budget := fs.Int("budget", 0, "mutants per symbol; 0 = exhaustive")
	timeout := fs.Duration("timeout", 60*time.Second, "one mutant's oracle run budget")
	jobs := fs.Int("jobs", 0, "concurrent mutant runs; 0 = half the CPUs")
	force := fs.Bool("force", false, "re-measure targets whose prior finding still covers")
	changed := fs.String("changed", "", "target only symbols whose bodies differ from this git ref")
	targetsFile := fs.String("targets", "", "JSON targets document; overrides discovery")
	findingsFile := fs.String("findings", defaultFindings, "findings document to read and update")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tree, err := gomutant.Load(*dir)
	if err != nil {
		return err
	}

	var targets []gomutant.Target
	var residue []gomutant.Residue
	switch {
	case *targetsFile != "":
		data, err := os.ReadFile(*targetsFile)
		if err != nil {
			return err
		}
		if targets, err = gomutant.LoadTargets(data); err != nil {
			return err
		}
	case *changed != "":
		paths, err := gitref.ChangedPaths(*dir, *changed)
		if err != nil {
			return err
		}
		targets, residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
			return gitref.Show(*dir, *changed, p)
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
	docPath := findingsAt(*dir, *findingsFile)
	prior, err := loadFindings(docPath)
	if err != nil {
		return err
	}
	findings, err := tree.Run(context.Background(), targets, gomutant.Options{
		Budget: *budget, Timeout: *timeout, Jobs: *jobs, Force: *force, Prior: prior,
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

func cmdFindings(args []string) error {
	fs := flag.NewFlagSet("findings", flag.ContinueOnError)
	dir := fs.String("dir", ".", "tree root the default document anchors at")
	findingsFile := fs.String("findings", defaultFindings, "findings document to read")
	label := fs.String("label", "", "show only findings carrying this label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	all, err := loadFindings(findingsAt(*dir, *findingsFile))
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
			if *label != "" && l != *label {
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

func cmdAttest(args []string) error {
	fs := flag.NewFlagSet("attest", flag.ContinueOnError)
	dir := fs.String("dir", ".", "tree root the default document anchors at")
	findingsFile := fs.String("findings", defaultFindings, "findings document to update")
	symbol := fs.String("symbol", "", "the mutated symbol")
	position := fs.String("position", "", "the survivor's position (file:line:col)")
	operator := fs.String("operator", "", "the survivor's operator")
	reason := fs.String("reason", "", "why the mutant is equivalent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" || *position == "" || *operator == "" || *reason == "" {
		return fmt.Errorf("attest needs -symbol, -position, -operator, and -reason")
	}
	return gomutant.UpdateDocument(findingsAt(*dir, *findingsFile), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == *symbol {
				return all, all[i].Attest(*position, *operator, *reason)
			}
		}
		return nil, fmt.Errorf("no finding for %s", *symbol)
	})
}

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dir := fs.String("dir", ".", "tree root (module or workspace)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return mcpserver.New(*dir).Run(context.Background())
}

func cmdEphemeral(args []string) error {
	fs := flag.NewFlagSet("ephemeral", flag.ContinueOnError)
	dir := fs.String("dir", ".", "tree root (module or workspace)")
	file := fs.String("file", "", "tree-relative source file to replace")
	replacement := fs.String("replacement", "", "path to the whole replacement source")
	testPkg := fs.String("test-pkg", "", "package whose named test decides the kill")
	runPat := fs.String("run", "", "-run pattern naming the deciding test")
	timeout := fs.Duration("timeout", 60*time.Second, "the run's budget")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *replacement == "" || *testPkg == "" || *runPat == "" {
		return fmt.Errorf("ephemeral needs -file, -replacement, -test-pkg, and -run")
	}
	mutant, err := os.ReadFile(*replacement)
	if err != nil {
		return err
	}
	tree, err := gomutant.Load(*dir)
	if err != nil {
		return err
	}
	res, err := tree.Ephemeral(context.Background(), *file, mutant, *testPkg, *runPat, *timeout)
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
