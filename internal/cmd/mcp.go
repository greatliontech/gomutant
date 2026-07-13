package cmd

import (
	"github.com/greatliontech/gomutant/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	dir := "."
	cmd := &cobra.Command{Use: "mcp", Short: "Serve gomutant over MCP", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return mcpserver.New(dir).Run(cmd.Context())
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "tree root (module or workspace)")
	return cmd
}
