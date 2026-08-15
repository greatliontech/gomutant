package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// Both version surfaces state the binary identity and the findings
// document versions - the capability line a version-skew field report
// needs (a long-lived server older than the CLI refuses the document
// by exactly this range).
func TestVersionSurfaces(t *testing.T) {
	want := fmt.Sprintf("findings document version %d, reads %d-%d", gomutant.DocumentVersion, gomutant.OldestReadableDocumentVersion, gomutant.DocumentVersion)
	if got := versionString(); !strings.Contains(got, want) {
		t.Fatalf("versionString() = %q, want it to carry %q", got, want)
	}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var buf bytes.Buffer
		cmd := newRootCommand()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%v output = %q, want the document-version line", args, buf.String())
		}
	}
}
