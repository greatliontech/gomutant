// Package cmd defines gomutant's Cobra command tree over the public library.
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

const defaultFindings = ".gomutant/findings.json"

// Execute runs the gomutant command tree with args.
func Execute(args []string) error {
	return ExecuteContext(context.Background(), args)
}

// ExecuteContext runs the gomutant command tree with args and cancellation.
func ExecuteContext(ctx context.Context, args []string) error {
	cmd := newRootCommand()
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
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
	cmd.AddCommand(newRunCommand(), newDiscoverCommand(), newFindingsCommand(), newAttestCommand(), newEphemeralCommand(), newMCPCommand())
	return cmd
}
