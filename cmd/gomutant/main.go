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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := internalcmd.ExecuteContext(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}
