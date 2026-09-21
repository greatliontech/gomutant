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
		var schema servedSchema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		params := guidance.Registered{}
		// Every served description at every depth — a nested object's
		// fields and an array item's fields alike — is the document's
		// rendering, the knob's terse clause, never a second literal;
		// the served bytes are walked here, independently of the
		// package's walk, and every name met is registered.
		var walk func(path string, node servedSchema)
		walk = func(path string, node servedSchema) {
			// The clause's premise, checked: no served schema carries a
			// shape the walk does not reach — a map-typed knob (an object
			// under additionalProperties) or a variant (anyOf/oneOf) would
			// hide nested prose from this walk and the package's alike.
			if len(node.AdditionalProperties) > 0 && node.AdditionalProperties[0] == '{' {
				t.Errorf("%s.%s carries a map-typed shape the walk does not reach: %s", tool.Name, path, node.AdditionalProperties)
			}
			if len(node.AnyOf)+len(node.OneOf) > 0 {
				t.Errorf("%s.%s carries a variant shape the walk does not reach", tool.Name, path)
			}
			for name, prop := range node.Properties {
				params[name] = false // the wire face prints no defaults
				k, err := doc.Knob("mcp", tool.Name, name)
				if err != nil {
					t.Errorf("%s.%s%s: %v", tool.Name, path, name, err)
					continue
				}
				if prop.Description != k.Clause() {
					t.Errorf("%s.%s%s description diverged from the document's rendering:\nwire %q\ndoc  %q", tool.Name, path, name, prop.Description, k.Clause())
				}
				walk(path+name+".", prop)
			}
			if node.Items != nil {
				walk(path+"[].", *node.Items)
			}
		}
		walk("", schema)
		if tool.Name == "run" {
			if got := schema.Properties["budget"].Description; got != "candidates per symbol (0 means exhaustive)" {
				t.Errorf("run.budget description = %q, want the literal terse clause", got)
			}
		}
		if tool.Name == "ephemeral" {
			// A nested field's description, pinned by its literal.
			items := schema.Properties["batch_edits"].Items
			if items == nil || items.Properties["old_string"].Description != "a batch edit's exact text to replace, matching exactly once in its file's original snapshot" {
				t.Errorf("ephemeral.batch_edits[].old_string description = %+v", items)
			}
			if items == nil || items.Properties["file"].Description != "tree-relative source file for replacement or edits" {
				t.Errorf("ephemeral.batch_edits[].file description = %+v", items)
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
func TestDescribeGuidanceSchemaRefusesAnUndocumentedProperty(t *testing.T) {
	refusal := func(schema *jsonschema.Schema) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		gomutant.DescribeGuidanceSchema("run", schemaNode{schema})
		return ""
	}
	top := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"nonesuch": {Type: "string"}}}
	if got := refusal(top); got != `gomutant: guidance: verb "run" documents no knob "nonesuch" on the mcp surface` {
		t.Fatalf("an undocumented property: recovered %q", got)
	}
	// A nested item's field is judged the same way, at its depth.
	nested := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"budget": {Type: "array", Items: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"nonesuch": {Type: "string"}}}}}}
	if got := refusal(nested); got != `gomutant: guidance: verb "run" documents no knob "nonesuch" on the mcp surface` {
		t.Fatalf("an undocumented nested property: recovered %q", got)
	}
}

// servedSchema is the wire shape the coverage walk reads: properties
// and array items at every depth, each with its description.
type servedSchema struct {
	Description          string                  `json:"description"`
	Properties           map[string]servedSchema `json:"properties"`
	Items                *servedSchema           `json:"items"`
	AdditionalProperties json.RawMessage         `json:"additionalProperties"`
	AnyOf                []servedSchema          `json:"anyOf"`
	OneOf                []servedSchema          `json:"oneOf"`
}
