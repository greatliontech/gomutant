package cmd

import (
	"fmt"
	"github.com/greatliontech/gofresh/toolversion"

	"github.com/spf13/cobra"

	gomutant "github.com/greatliontech/gomutant"
)

// versionString combines embedded executable identity with the separate
// findings-format capability. Neither is inferred from the current project.
func versionString() string {
	return fmt.Sprintf("gomutant %s (findings document version %d, reads %d-%d)",
		toolversion.Identity(), gomutant.DocumentVersion, gomutant.OldestReadableDocumentVersion, gomutant.DocumentVersion)
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: guidanceShort("version"), Long: guidanceHelp("version"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return err
		},
	}
}
