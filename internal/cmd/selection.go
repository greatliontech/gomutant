package cmd

import (
	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/pflag"
)

// selectionFlags registers the build-selection surface every
// tree-loading command shares: declared tags and a toolchain directive
// rewrite the tree's one frozen environment at load, so discovery,
// resolution, oracle spawns, and the measurement pins all see the same
// selection by construction. Declared tags replace any ambient GOFLAGS
// -tags; the toolchain directive replaces GOTOOLCHAIN.
func selectionFlags(f *pflag.FlagSet, tags *[]string, toolchain *string) {
	// The usages are the document's rendering, set at the verb's
	// construction (knobbedFlags).
	f.StringArrayVar(tags, "tag", nil, "")
	f.StringVar(toolchain, "toolchain", "", "")
}

func selectionOf(tags []string, toolchain string) gomutant.Selection {
	return gomutant.Selection{Tags: tags, Toolchain: toolchain}
}
