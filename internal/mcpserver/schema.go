package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/gomutant"
)

// knobbedTool is a tool whose input schema's every property description
// is the guidance document's knob prose rendered — its terse clause —
// under the tool's mcp spelling, never a second literal; a knob the
// document does not carry refuses at construction, so the served set
// cannot outgrow the document silently (REQ-mcp-guidance).
func knobbedTool[In any](verb string) *mcp.Tool {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic("mcpserver: input schema for " + verb + ": " + err.Error())
	}
	knobSchema(verb, schema)
	return &mcp.Tool{Name: verb, Description: guidanceDescription(verb), InputSchema: schema}
}

// knobSchema renders every top-level property description — the
// tool's knobs, the names the coverage judgment enumerates. A nested
// item's fields (an edit's file, old_string, new_string) are not knobs
// and carry no description of their own: the knob's prose describes
// the item's shape.
func knobSchema(verb string, schema *jsonschema.Schema) {
	for name, prop := range schema.Properties {
		prop.Description = gomutant.KnobClause("mcp", verb, name)
	}
}
