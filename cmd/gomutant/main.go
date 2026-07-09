// Command gomutant is the CLI over the gomutant library: it discovers or
// loads targets, runs mutants, maintains the findings document, and
// dispositions survivors. Findings are advisory (REQ-result-findings):
// the exit code reports operational failure, never open findings — build
// policy over findings belongs to the caller's scripting.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gomutant "github.com/greatliontech/gomutant"
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
		return fmt.Errorf("usage: gomutant <run|findings|attest|ephemeral> [flags]")
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
		if targets, err = gomutant.ParseTargets(data); err != nil {
			return err
		}
	case *changed != "":
		paths, err := changedPaths(*dir, *changed)
		if err != nil {
			return err
		}
		targets, residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
			return gitShow(*dir, *changed, p)
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

	prior, err := loadFindings(*findingsFile)
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
	return saveFindings(*findingsFile, prior, findings)
}

func cmdFindings(args []string) error {
	fs := flag.NewFlagSet("findings", flag.ContinueOnError)
	findingsFile := fs.String("findings", defaultFindings, "findings document to read")
	label := fs.String("label", "", "show only findings carrying this label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	all, err := loadFindings(*findingsFile)
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
	all, err := loadFindings(*findingsFile)
	if err != nil {
		return err
	}
	idx := -1
	for i := range all {
		if all[i].Symbol == *symbol {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no finding for %s", *symbol)
	}
	if err := all[idx].Attest(*position, *operator, *reason); err != nil {
		return err
	}
	return writeFindings(*findingsFile, all)
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

// saveFindings merges a run's findings over the prior document — a measured
// or cached finding replaces its symbol's record, untouched symbols stay —
// and writes it back.
func saveFindings(path string, prior, fresh []gomutant.Finding) error {
	bySym := map[string]gomutant.Finding{}
	for _, f := range prior {
		bySym[f.Symbol] = f
	}
	// Skipped results are excluded by Export, the single owner of that rule.
	for _, f := range fresh {
		bySym[f.Symbol] = f
	}
	merged := make([]gomutant.Finding, 0, len(bySym))
	for _, f := range bySym {
		merged = append(merged, f)
	}
	return writeFindings(path, merged)
}

func writeFindings(path string, findings []gomutant.Finding) error {
	doc, err := gomutant.Export(findings)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(doc, '\n'), 0o644)
}

// changedPaths lists tree-relative paths differing from ref, via git:
// tracked changes (--relative keeps them tree-relative when the tree is not
// the repo root) plus untracked files — a brand-new uncommitted file is part
// of the changed surface, never silently absent (REQ-target-changed).
// quotepath is off so a non-ASCII path arrives as bytes, not an escaped
// quoted string that would misclassify as non-Go.
func changedPaths(dir, ref string) ([]string, error) {
	tracked, err := gitOutput(dir, "-c", "core.quotepath=off", "diff", "--name-only", "--relative", ref)
	if err != nil {
		return nil, err
	}
	untracked, err := gitOutput(dir, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, out := range [][]byte{tracked, untracked} {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" && !seen[line] {
				seen[line] = true
				paths = append(paths, line)
			}
		}
	}
	return paths, nil
}

// gitShow reads a tree-relative path's content at ref; ok=false when the
// path did not exist there (a new file reads as all changed). The ./ form
// resolves the path against the command's directory, so it stays correct
// when the tree is not the repo root.
func gitShow(dir, ref, path string) ([]byte, bool) {
	out, err := gitOutput(dir, "show", ref+":./"+path)
	if err != nil {
		return nil, false
	}
	return out, true
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
