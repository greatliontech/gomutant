package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

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
