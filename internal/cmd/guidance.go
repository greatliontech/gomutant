package cmd

import (
	"fmt"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// guidanceShort and guidanceHelp are a command's served prose under
// its cli spelling — the guidance registration's purpose and its
// knobless help (cobra renders its own Flags: block, so the full
// knobs: block would print every knob twice in two wordings) — read
// from the document at construction, never a second literal
// (REQ-mcp-guidance).
func guidanceShort(verb string) string {
	return gomutant.GuidanceRegistration("cli", verb).Description
}

func guidanceHelp(verb string) string {
	return gomutant.GuidanceRegistration("cli", verb).Help
}

// knobbedFlags renders a command's own flags' usage from the document
// under the cli spelling — each knob's usage rendering, gofresh's
// grammar for pflag (code spans unquoted, the default parenthetical
// cobra prints itself dropped), never a second literal — at the
// command's construction, so every constructor's command serves the
// document whether or not it hangs under the root, and appends the
// registration's pointer to the knobs' whole prose; a flag the
// document does not carry refuses at construction with the package's
// wording. The command's own set, not LocalFlags: a parentless
// command's LocalFlags absorbs pflag.CommandLine, which this face
// does not own (REQ-mcp-guidance).
func knobbedFlags(cmd *cobra.Command, verb string) *cobra.Command {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		f.Usage = gomutant.GuidanceKnob("cli", verb, f.Name).Usage()
	})
	// Every caller registers flags and has set Long (the knobless
	// help) before this call, so the pointer rides here: a knobless
	// verb (version, guidance) never reaches it, and the Long-vs-Help
	// judgment refuses a constructor that sets Long afterwards.
	cmd.Long += "\n\n" + gomutant.GuidanceRegistration("cli", verb).ProsePointer
	return cmd
}

// newGuidanceCommand serves the guidance document itself: a verb's
// full section, or the decision map for orientation.
func newGuidanceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "guidance [verb]",
		Short: guidanceShort("guidance"),
		Long:  guidanceHelp("guidance"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), gomutant.Guidance().Orientation())
				return nil
			}
			long, err := gomutant.Guidance().Long("cli", args[0])
			if err != nil {
				return fmt.Errorf("%w; run guidance with no verb for the decision map, which names every verb", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), long)
			return nil
		},
	}
}
