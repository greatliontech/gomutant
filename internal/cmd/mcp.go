package cmd

import (
	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	dir := "."
	var vouches []string
	cmd := &cobra.Command{Use: "mcp", Short: guidanceShort("mcp"), Long: guidanceHelp("mcp"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
	cmd.Flags().StringVar(&dir, "dir", ".", "")
	cmd.Flags().StringArrayVar(&vouches, "vouch", nil, "")
	return knobbedFlags(cmd, "mcp")
}
