// Command gomutant is the CLI over the gomutant library. Findings are
// advisory: its exit status reports operational failure, never open findings.
package main

import (
	"fmt"
	"os"

	internalcmd "github.com/greatliontech/gomutant/internal/cmd"
)

func main() {
	if err := internalcmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gomutant:", err)
		os.Exit(1)
	}
}
