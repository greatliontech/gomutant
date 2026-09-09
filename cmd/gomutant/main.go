// Command gomutant is the CLI over the gomutant library. Findings are
// advisory: its exit status reports operational failure, never open findings.
package main

import (
	"context"
	"errors"
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
		os.Exit(exitCode(err))
	}
}

// exitCode is the tree's own exit status where the error carries one —
// an MCP session ended on its transport says 2 (REQ-mcp-exit-log) —
// and the one failure code otherwise. The method is the tree's own
// name: a subprocess's status (os.ProcessState.ExitCode, promoted by
// every exec.ExitError the tree wraps) never becomes the command's.
func exitCode(err error) int {
	var coded interface{ MCPExitCode() int }
	if errors.As(err, &coded) {
		return coded.MCPExitCode()
	}
	return 1
}
