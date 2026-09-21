package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/greatliontech/gofresh/guidance"
	gomutant "github.com/greatliontech/gomutant"
)

// guidanceSession is an in-memory client session over a fresh server.
func guidanceSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := New(t.TempDir()).MCP()
	ct, tr := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), tr) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// guidanceText is a result's single text content.
func guidanceText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content = %d parts, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want text", res.Content[0])
	}
	return tc.Text
}

// The embedded guidance document parses — a malformed document would
// panic every serving surface, so this is the loud build-time pin
// (REQ-mcp-guidance).
func TestGuidanceDocumentParses(t *testing.T) {
	if _, err := gomutant.GuidanceDocument(); err != nil {
		t.Fatal(err)
	}
}

// The wire surface and the guidance document cannot drift: the
// initialize-result instructions ARE the decision map, every listed
// tool's name and every input-schema property is documented in both
// directions, and each served description is the document's
// one-liner — identity, not resemblance (REQ-mcp-guidance).
func TestGuidanceCoversTheWireSurface(t *testing.T) {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	sess := guidanceSession(t)
	if got := sess.InitializeResult().Instructions; got != doc.Orientation() {
		t.Fatalf("wire instructions diverged from the decision map:\n%q", got)
	}
	list, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]guidance.Registered{}
	for _, tool := range list.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		params := guidance.Registered{}
		for name, prop := range schema.Properties {
			params[name] = false // the wire face prints no defaults
			// Every served description is the document's rendering — the
			// knob's terse clause — never a second literal.
			k, err := doc.Knob("mcp", tool.Name, name)
			if err != nil {
				t.Errorf("%s.%s: %v", tool.Name, name, err)
				continue
			}
			if prop.Description != k.Clause() {
				t.Errorf("%s.%s description diverged from the document's rendering:\nwire %q\ndoc  %q", tool.Name, name, prop.Description, k.Clause())
			}
			if tool.Name == "run" && name == "budget" && prop.Description != "candidates per symbol (0 means exhaustive)" {
				t.Errorf("run.budget description = %q, want the literal terse clause", prop.Description)
			}
		}
		registered[tool.Name] = params
		want, err := doc.Description("mcp", tool.Name)
		if err != nil {
			t.Errorf("tool %q: %v", tool.Name, err)
			continue
		}
		if tool.Description != want {
			t.Errorf("tool %q description diverged from the document:\nwire %q\ndoc  %q", tool.Name, tool.Description, want)
		}
	}
	if defects, err := doc.Coverage("mcp", registered); err != nil || len(defects) != 0 {
		t.Fatalf("mcp coverage: err=%v defects:\n%s", err, strings.Join(defects, "\n"))
	}
}

// The guidance tool serves the document: a verb's long section, the
// decision map for the empty verb, and a teaching error for an
// unknown one (REQ-mcp-guidance).
func TestGuidanceToolServesTheDocument(t *testing.T) {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	sess := guidanceSession(t)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{"verb": "run"}})
	if err != nil {
		t.Fatal(err)
	}
	long, err := doc.Long("mcp", "run")
	if err != nil {
		t.Fatal(err)
	}
	if got := guidanceText(t, res); got != long {
		t.Fatalf("guidance(run) diverged from the document:\n%q\nwant\n%q", got, long)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := guidanceText(t, res); got != doc.Orientation() {
		t.Fatalf("guidance() diverged from the decision map:\n%q", got)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{"verb": "vanished"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown verb served instead of erroring")
	}
	if got := guidanceText(t, res); !strings.Contains(got, "decision map") {
		t.Fatalf("unknown-verb error teaches nothing: %q", got)
	}
}

// A schema property the document does not carry refuses the tool's
// construction: the served set cannot outgrow the document silently
// (REQ-mcp-guidance).
func TestKnobSchemaRefusesAnUndocumentedProperty(t *testing.T) {
	schema := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"nonesuch": {Type: "string"}}}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "nonesuch") {
			t.Fatalf("knobSchema over an undocumented property: recovered %v, want a refusal naming nonesuch", r)
		}
	}()
	knobSchema("run", schema)
}
