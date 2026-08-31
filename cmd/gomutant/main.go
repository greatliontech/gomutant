// Command gomutant is the CLI over the gomutant library. Findings are
// advisory: its exit status reports operational failure, never open findings.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	internalcmd "github.com/greatliontech/gomutant/internal/cmd"
)

func main() {
	// SIGTERM is the process-level hard cancel; SIGINT routes through
	// the command tree's two-stage policy (first interrupt drains a
	// running campaign, the second cancels hard) installed by
	// ExecuteContext.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := internalcmd.ExecuteContext(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}
