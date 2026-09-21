package gomutant

import (
	_ "embed"

	"github.com/greatliontech/gofresh/guidance"
)

// docs/guidance.md is the single home of verb-level served prose
// (what a verb does, what a knob controls, when to use which),
// embedded because the binary travels while the repository stays
// home, and parsed once for every surface to project from (gofresh
// docs/specs/guidance.md is the format contract; this module's
// serving contract is REQ-mcp-guidance in docs/specs/mcp.md).
//
//go:embed docs/guidance.md
var guidanceSrc []byte

// embeddedGuidance is the source parsed once for every surface to
// project from; Must refuses a malformed document loudly, naming this
// tool, where Document answers the parse error.
var embeddedGuidance = guidance.Embed("gomutant", guidanceSrc)

// Guidance is the embedded guidance document every face reads: a
// malformed document is a build defect the parse-pinning test
// surfaces, so a face's construction fails loudly rather than serving
// nothing (REQ-mcp-guidance).
func Guidance() *guidance.Document { return embeddedGuidance.Must() }

// GuidanceDocument is the parsed embedded guidance source, parsed
// once; a malformed document is a build-time defect every consumer
// surfaces loudly.
func GuidanceDocument() (*guidance.Document, error) { return embeddedGuidance.Document() }

// GuidanceKnob is one knob under a face's spelling, read at the face's
// construction: a knob the document does not carry is a build defect
// — the coverage judgments name it — refused loudly by the package
// ("gomutant: guidance: <cause>") rather than served empty
// (REQ-mcp-guidance).
func GuidanceKnob(face, verb, name string) guidance.Knob {
	return embeddedGuidance.MustKnob(face, verb, name)
}

// GuidanceRegistration is a verb's registration under a face's
// spelling — its purpose, help, long rendering, knobs, and the prose
// pointer — read at the face's construction with the same refusal
// (REQ-mcp-guidance).
func GuidanceRegistration(face, verb string) guidance.Registration {
	return embeddedGuidance.MustRegistration(face, verb)
}

// DescribeGuidanceSchema describes a served input schema's every
// property the walk reaches — a nested object's properties and an
// array's items — with the verb's knob of the property's own name
// under the mcp spelling, refusing a property the document does not
// knob; the wire face's coverage judgment enumerates the served
// schema itself, so the walk's own name list is not read here
// (REQ-mcp-guidance).
func DescribeGuidanceSchema(verb string, root guidance.SchemaNode) {
	embeddedGuidance.MustDescribeSchema("mcp", verb, root)
}
