package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	gomutant "github.com/greatliontech/gomutant"
)

// versionString is the binary's field-report identity: the module
// version when installed from a release, else the VCS revision with a
// dirty marker (a from-source install carries no module version), plus
// the findings document versions this binary writes and reads - the
// capability line a version-skew report needs, because a long-lived
// server older than the CLI that wrote the document refuses it by
// exactly this range.
func versionString() string {
	identity := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			identity = info.Main.Version
		}
		var revision, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			}
		}
		if (identity == "(devel)" || identity == "unknown") && revision != "" {
			if len(revision) > 12 {
				revision = revision[:12]
			}
			identity = revision + dirty
		}
	}
	return fmt.Sprintf("gomutant %s (findings document version %d, reads %d-%d)",
		identity, gomutant.DocumentVersion, gomutant.OldestReadableDocumentVersion, gomutant.DocumentVersion)
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: guidanceShort("version"), Long: guidanceHelp("version"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return nil
		},
	}
}
