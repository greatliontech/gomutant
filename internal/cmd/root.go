// Package cmd defines gomutant's Cobra command tree over the public library.
package cmd

import (
	"context"
	"fmt"

	"github.com/greatliontech/gofresh/resident"
	"github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

const defaultFindings = gomutant.DefaultFindingsPath

// Execute runs the gomutant command tree with args.
func Execute(args []string) error {
	return ExecuteContext(context.Background(), args)
}

// ExecuteContext runs the gomutant command tree with args and cancellation.
func ExecuteContext(ctx context.Context, args []string) error {
	startDebugProfiler()
	ctx, stop := withSoftInterrupt(ctx)
	defer stop()
	cmd := newRootCommand()
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gomutant",
		Short:         "Mutation testing for Go",
		Version:       versionString(),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("a command is required")
		},
		// Every command's process runs under the fleet's one ceiling
		// (gofresh/resident: the host's available memory halved,
		// floored at 1 GiB; an explicit operator GOMEMLIMIT replacing
		// it), installed before the verb runs — REQ-mcp-resident-set.
		PersistentPreRun: func(*cobra.Command, []string) {
			limit := resident.InstallCeiling()
			if seams.memoryLimitInstalled != nil {
				seams.memoryLimitInstalled(limit)
			}
		},
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.AddCommand(newRunCommand(), newDiscoverCommand(), newFindingsCommand(), newAttestCommand(), newPruneCommand(), newRetargetCommand(), newEphemeralCommand(), newMCPCommand(), newVersionCommand(), newGuidanceCommand())
	return cmd
}
