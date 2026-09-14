package mcpserver

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/spectable"
)

// surfaceTable is REQ-mcp-surfaces' table: the section of mcp.md from
// the requirement to the next, so no other table's rows are read.
func surfaceTable(t *testing.T) string {
	t.Helper()
	return spectable.Section(t, filepath.Join("..", "..", "docs", "specs", "mcp.md"), "REQ-mcp-surfaces")
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
	// The MCP face keys the bounds of its own column and the shared one;
	// the CLI column is the CLI face's to key (internal/cmd).
	table := spectable.Columns(t, surfaceTable(t), 4, 5)
	keyed := []struct {
		phrase *regexp.Regexp
		want   int
	}{
		{regexp.MustCompile(`(?:rows?|record|attestations inline[^\n|]*?|groups|rewrites) (?:capped )?at (\d+)`), envelope.rows},
		{regexp.MustCompile(`symbols per group at (\d+)`), envelope.nested},
		{regexp.MustCompile(`open survivors at (\d+)`), envelope.open},
		{regexp.MustCompile(`open survivors and clauses at (\d+)`), envelope.reasons},
		{regexp.MustCompile(`not-reusable roster at (\d+)`), gomutant.PostureCap},
		{regexp.MustCompile(`analysis events likewise, inline at (\d+)`), envelope.rows},
	}
	seen := map[int]bool{}
	for _, k := range keyed {
		for _, m := range k.phrase.FindAllStringSubmatchIndex(table, -1) {
			n, _ := strconv.Atoi(table[m[2]:m[3]])
			if n != k.want {
				t.Fatalf("the surface table states %q with %d; the policy holds %d", table[m[0]:m[1]], n, k.want)
			}
			seen[m[2]] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("the surface table's MCP columns state no bound")
	}
	for _, m := range regexp.MustCompile(`\b(?:at|to) (\d+)\b`).FindAllStringSubmatchIndex(table, -1) {
		if !seen[m[2]] {
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
}

// The envelope clause states the same bounds in prose: every row cap
// it names is the policy's row bound, every per-record survivor cap the
// open bound (REQ-mcp-envelope).
func TestEnvelopeClauseStatesTheHeldBounds(t *testing.T) {
	section := spectable.Section(t, filepath.Join("..", "..", "docs", "specs", "mcp.md"), "REQ-mcp-envelope")
	open := regexp.MustCompile(`open survivors per finding at (\d+)`)
	seen := map[int]bool{}
	for _, m := range open.FindAllStringSubmatchIndex(section, -1) {
		if n, _ := strconv.Atoi(section[m[2]:m[3]]); n != envelope.open {
			t.Fatalf("the envelope clause states %q; the policy holds %d", section[m[0]:m[1]], envelope.open)
		}
		seen[m[2]] = true
	}
	stated := 0
	for _, m := range regexp.MustCompile(`\bat (\d+)\b`).FindAllStringSubmatchIndex(section, -1) {
		stated++
		if seen[m[2]] {
			continue
		}
		if n, _ := strconv.Atoi(section[m[2]:m[3]]); n != envelope.rows {
			t.Fatalf("the envelope clause states a bound %q that is neither the row bound %d nor a keyed one", section[m[0]:m[1]], envelope.rows)
		}
	}
	if stated == 0 || len(seen) == 0 {
		t.Fatalf("the envelope clause states %d bounds, %d of them the open bound; want both kinds", stated, len(seen))
	}
}
