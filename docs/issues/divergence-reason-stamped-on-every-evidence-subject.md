# One target-side runtime reason is stamped onto every evidence subject

`run.go`'s post-splice divergence arm (the `diverged` block after
`spliced.rec`) copies the TARGET evidence's pre-splice `RuntimeReason`
onto `rec.TargetEvidence` and onto every `rec.OracleEvidence[i]`,
setting each unverifiable, while leaving every subject's `RuntimeInputs`
as the splice produced it. A record therefore carries one reason on 243
subjects beside empty manifests — the shape pb's field report
root-directory-input-with-empty-manifest (gofresh) called impossible —
and the reason on an oracle subject is not that subject's own
observation. With gofresh 259's attribution the stamped reason names
the target-side process, so the record still misattributes the oracle
subjects, now precisely.

Expected: a subject's runtime reason is its own observation's (the
target's divergence stays the target's, rendered once as the record's
divergence, not multiplied), or the record states the stamp as what it
is (a record-level divergence disposition) instead of per-subject
evidence.

The budget-cut validation stamp (cross-tool train chunk 257) shares the
shape through `stampUnverifiable`: the engine's analysis-unavailable
verdict names the first subject it could not re-establish, and the one
reason lands on the target's and every oracle's evidence alike — the
per-subject attribution this issue asks for covers both callers.

Lands: cross-tool train chunk 258 (the evidence walk's faults stamp their own clause).
