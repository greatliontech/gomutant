package cmd

import (
	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	dir := "."
	var vouches []string
	cmd := &cobra.Command{Use: "mcp", Short: "Serve gomutant over MCP", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var opts []mcpserver.Option
		if len(vouches) > 0 {
			identities, err := gomutant.ParseDynamicStateVouches(vouches)
			if err != nil {
				return err
			}
			opts = append(opts, mcpserver.WithDynamicStateVouches(identities...))
		}
		return mcpserver.New(dir, opts...).Run(cmd.Context())
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "tree root (module or workspace)")
	cmd.Flags().StringArrayVar(&vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; every tool call's analysis judges under the server's set - a per-server input because the loaded tree is shared across calls")
	return cmd
}
