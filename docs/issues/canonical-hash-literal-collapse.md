# Canonical body hash collapses literal contents

Lands: when the canonical text projection preserves string, rune, and comment-free
literal contents, or the changed-scope comparison otherwise distinguishes a
literal-content change from formatting churn.

## Observed

`canonText` applies Unicode NFC and `strings.Fields` whitespace collapsing to the
raw declaration source, including the interior of string literals
(`internal/engine/symbol.go:56-58`). The projection backs `BodyHash`
(`internal/engine/symbol.go:27-49`) and the changed-scope surface comparison
(`internal/engine/surface.go:107,175`).

Behaviorally different literals such as `"a  b"` and `"a b"` therefore share a
canonical hash. A commit whose only change is literal-interior whitespace is
classified as formatting-only churn: the changed-scope mode surfaces no target
for it (`docs/specs/targeting.md` REQ-target-changed promises "formatting churn
yields none", but a literal-content change is not formatting churn), and the
same projection decides the "canonical body equals the baseline" discard rule
(`docs/specs/mutation.md`, candidate discard paragraph).

Finding reuse is not affected: reuse pins the Gofresh maximal closure computed
over exact declaration bytes (`findings.go` SubjectEvidence,
`freshness.go` evidence matching), so a literal change stales a persisted
finding even though its body hash collides. The defect is confined to
changed-scope target selection and canonical-equality discards.

## Resolution

Make the canonical projection literal-aware — collapse whitespace only outside
literal (and rune) tokens — or compare literal-bearing spans byte-exactly in the
changed-scope diff, so a literal-content edit surfaces as a changed body while
pure formatting churn still yields no target.
