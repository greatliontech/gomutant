package cmd

import (
	"fmt"
	"regexp"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// knobbedFlags renders a command's own flags' usage from the document
// under the cli spelling — each knob's terse clause in pflag's usage
// grammar, never a second literal — at the command's construction, so
// every constructor's command serves the document whether or not it
// hangs under the root, and appends the long help's pointer to the
// knobs' whole prose; a flag the document does not carry refuses at
// construction. The command's own set, not LocalFlags: a parentless
// command's LocalFlags absorbs pflag.CommandLine, which this face
// does not own (REQ-mcp-guidance).
func knobbedFlags(cmd *cobra.Command, verb string) *cobra.Command {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		f.Usage = flagUsage(gomutant.KnobClause("cli", verb, f.Name))
	})
	// Every caller registers flags and has set Long (the knobless
	// help) before this call, so the pointer rides here: a knobless
	// verb (version, guidance) never reaches it, and the Long-vs-Help
	// judgment refuses a constructor that sets Long afterwards.
	cmd.Long += "\n\n" + knobProsePointer(verb)
	return cmd
}

// defaultParenthetical is the document's spelling of a knob's default
// inside its clause; the cli face prints a flag's default itself.
var defaultParenthetical = regexp.MustCompile(` \(default [^)]*\)`)

// flagUsage is a knob's clause in pflag's usage grammar: pflag reads
// the first back-quoted word of a usage string as the flag's value
// name (a `attest` span would print --reattest as value-taking), so
// the document's code spans lose their quotes; and cobra appends a
// flag's non-zero default, so the clause's own default parenthetical
// goes, or the default prints twice. The document's rule for a knob
// whose cli default is non-zero: spell the default as "(default X)"
// — the one form this grammar strips — and use the word "default"
// nowhere else in the first clause; the coverage judgment refuses
// any other spelling.
func flagUsage(clause string) string {
	return strings.ReplaceAll(defaultParenthetical.ReplaceAllString(clause, ""), "`", "")
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

// knobProsePointer names the cli's served path to a verb's whole knob
// prose: a flag's usage is the knob's terse clause, the rest of the
// document's line is served by the guidance command alone. It rides
// the long help of a verb with cli knobs (knobbedFlags) — a knobless
// verb has no prose to point at.
func knobProsePointer(verb string) string {
	return "The knobs' whole prose: gomutant guidance " + verb + "."
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
