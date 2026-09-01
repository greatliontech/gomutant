// Command gomutant is the CLI over the gomutant library. Findings are
// advisory: its exit status reports operational failure, never open findings.
package main

import (
	"context"
	"fmt"
	"os"

	internalcmd "github.com/greatliontech/gomutant/internal/cmd"
)

func main() {
	// SIGINT and SIGTERM both route through the command tree's
	// two-stage policy installed by ExecuteContext: the first signal
	// drains a running campaign or cancels outright when no drain is
	// armed; a second of either cancels hard. A SIGTERM drain is
	// deadline-bounded so a supervisor's stop banks what fits its
	// kill window and then dies cleanly — never eating a SIGKILL with
	// orphaned oracle process trees.
	if err := internalcmd.ExecuteContext(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}
