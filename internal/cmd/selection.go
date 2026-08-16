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
	f.StringArrayVar(tags, "tag", nil, "build tag for this run's selection (repeatable); a //go:build-gated symbol or oracle under the tag measures exactly as an untagged one, and the declared set replaces any ambient GOFLAGS -tags")
	f.StringVar(toolchain, "toolchain", "", "GOTOOLCHAIN directive for this run's selection (e.g. go1.26.5 or a custom toolchain name); rides the toolchain measurement pin, so a different selection re-measures rather than serving across")
}

func selectionOf(tags []string, toolchain string) gomutant.Selection {
	return gomutant.Selection{Tags: tags, Toolchain: toolchain}
}
