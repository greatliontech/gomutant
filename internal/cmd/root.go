// Package cmd defines gomutant's Cobra command tree over the public library.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const defaultFindings = ".gomutant/findings.json"

// Execute runs the gomutant command tree with args.
func Execute(args []string) error {
	cmd := newRootCommand()
	cmd.SetArgs(args)
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
