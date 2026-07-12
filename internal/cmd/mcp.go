package cmd

import (
	"context"

	"github.com/greatliontech/gomutant/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	dir := "."
	cmd := &cobra.Command{Use: "mcp", Short: "Serve gomutant over MCP", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return mcpserver.New(dir).Run(context.Background())
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "tree root (module or workspace)")
	return cmd
}
