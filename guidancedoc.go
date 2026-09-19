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

// KnobClause is the served text of one knob under a face's spelling:
// the document's terse clause (gofresh's knob projection). A knob the
// document does not carry is a build defect — the coverage judgments
// name it — so the read refuses loudly at the face's construction
// rather than serving an empty description (REQ-mcp-guidance).
func KnobClause(face, verb, name string) string {
	k, err := Guidance().Knob(face, verb, name)
	if err != nil {
		panic("gomutant: guidance knob: " + err.Error())
	}
	return k.Clause()
}
