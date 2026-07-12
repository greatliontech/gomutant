package cmd

import (
	"fmt"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

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
