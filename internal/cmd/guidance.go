package cmd

import (
	"fmt"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

// guidanceShort and guidanceHelp are a command's served prose under
// its cli spelling, read from the guidance document at construction —
// never a second literal (REQ-mcp-guidance).
func guidanceShort(verb string) string {
	d, err := gomutant.Guidance().Description("cli", verb)
	if err != nil {
		panic("cmd: " + err.Error())
	}
	return d
}

// guidanceHelp is the knobless long rendering — cobra renders its
// own Flags: block, so the full knobs: block would print every knob
// twice in two wordings.
func guidanceHelp(verb string) string {
	l, err := gomutant.Guidance().Help("cli", verb)
	if err != nil {
		panic("cmd: " + err.Error())
	}
	return l
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
