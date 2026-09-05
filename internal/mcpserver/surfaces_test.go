package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// surfaceTable is REQ-mcp-surfaces' table: the section of mcp.md from
// the requirement to the next, so no other table's rows are read.
func surfaceTable(t *testing.T) string {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "specs", "mcp.md"))
	if err != nil {
		t.Fatal(err)
	}
	section := string(spec)
	start := strings.Index(section, "**REQ-mcp-surfaces**")
	if start < 0 {
		t.Fatal("REQ-mcp-surfaces not found in mcp.md")
	}
	section = section[start:]
	if end := strings.Index(section[1:], "**REQ-"); end >= 0 {
		section = section[:end+1]
	}
	return section
}

// The surface table REQ-mcp-surfaces records lists exactly the guidance
// document's verbs with exactly their faces (the faces in the document's
// order, "mcp, cli" for a verb on both), so a verb or a face added to
// one is added to the other.
func TestSurfaceTableTracksTheGuidanceVerbs(t *testing.T) {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, v := range doc.Verbs {
		faces := []string{}
		if len(v.Surfaces) == 0 {
			faces = []string{"mcp", "cli"}
		} else {
			for _, sn := range v.Surfaces {
				faces = append(faces, sn.Surface)
			}
		}
		want[v.Name] = strings.Join(faces, ", ")
	}
	rows := regexp.MustCompile(`(?m)^\| ([a-z_]+)(?: \([^)]*\))? \| ([a-z, ]+) \|`).FindAllStringSubmatch(surfaceTable(t), -1)
	got := map[string]string{}
	for _, row := range rows {
		if row[1] == "verb" {
			continue // the header
		}
		got[row[1]] = row[2]
	}
	for name, faces := range want {
		if got[name] != faces {
			t.Fatalf("surface table row for %s = %q; want the guidance document's faces %q", name, got[name], faces)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Fatalf("surface table lists %s, which the guidance document does not describe", name)
		}
	}
}

// Every bound and timeout the surface table states is the one the
// envelope policy or the command deadline holds, each phrase keyed to
// its own bound — rows, records, groups and inline attestations to the
// row bound; symbols per group to the nested bound; open survivors and
// clauses to their bounds — and "N seconds" to the MCP command deadline;
// a number after "at" or "to" that no phrase keys fails closed.
func TestSurfaceTableStatesTheHeldBounds(t *testing.T) {
	table := surfaceTable(t)
	keyed := []struct {
		phrase *regexp.Regexp
		want   int
	}{
		{regexp.MustCompile(`(?:rows?|record|attestations inline[^|]*?|groups|rewrites) (?:capped )?at (\d+)`), envelope.rows},
		{regexp.MustCompile(`symbols per group at (\d+)`), envelope.nested},
		{regexp.MustCompile(`open survivors at (\d+)`), envelope.open},
		{regexp.MustCompile(`open survivors and clauses at (\d+)`), envelope.reasons},
	}
	seen := map[string]bool{}
	for _, k := range keyed {
		for _, m := range k.phrase.FindAllStringSubmatchIndex(table, -1) {
			n, _ := strconv.Atoi(table[m[2]:m[3]])
			if n != k.want {
				t.Fatalf("the surface table states %q with %d; the policy holds %d", table[m[0]:m[1]], n, k.want)
			}
			seen[strconv.Itoa(m[2])] = true
		}
	}
	for _, m := range regexp.MustCompile(`\b(?:at|to) (\d+)\b`).FindAllStringSubmatchIndex(table, -1) {
		if !seen[strconv.Itoa(m[2])] {
			t.Fatalf("the surface table states a bound %q that no phrase keys to a policy field", table[m[0]:m[1]])
		}
	}
	if envelope.open != envelope.reasons {
		t.Fatalf("the open and clause bounds differ (%d, %d); the table's shared phrase must split", envelope.open, envelope.reasons)
	}
	seconds := regexp.MustCompile(`(\d+) seconds`).FindAllStringSubmatch(table, -1)
	if len(seconds) == 0 {
		t.Fatal("the surface table states no command deadline")
	}
	for _, m := range seconds {
		if n, _ := strconv.Atoi(m[1]); n != defaultCommandTimeoutSec {
			t.Fatalf("the surface table states a %d-second deadline; the MCP command deadline is %d", n, defaultCommandTimeoutSec)
		}
	}
	if !strings.Contains(table, `{"edits":[`) {
		t.Fatal("the surface table does not state the CLI edit-batch shape")
	}
}
