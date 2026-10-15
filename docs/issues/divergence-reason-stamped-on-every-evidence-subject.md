# One target-side runtime reason is stamped onto every evidence subject

The historical `run.go` post-splice divergence arm (the `diverged` block after
`spliced.rec`) copied the TARGET evidence's pre-splice `RuntimeReason`
onto `rec.TargetEvidence` and onto every `rec.OracleEvidence[i]`,
setting each unverifiable, while leaving every subject's `RuntimeInputs`
as the splice produced it. The reported record carried one reason on 243
subjects beside empty manifests — the shape pb's field report
root-directory-input-with-empty-manifest (gofresh) called impossible —
and the reason on an oracle subject was not that subject's own
observation. The attribution added later named the target-side process on
those rows too; that historical defect is distinct from the current residual.

Current `stampUnverifiable` clears `RuntimeAttribution` when replacing the
reason. The stale target-process attribution described above is repaired, and
changed-oracle splicing has no admitted producer under full-oracle@3. The
remaining problem is record-wide failure represented by rewriting every
subject's runtime reason; the analysis-unavailable stamp still has this shape.

The budget-cut validation stamp shares the
shape through `stampUnverifiable`: the engine's analysis-unavailable
verdict names the first subject it could not re-establish, and the one
reason lands on the target's and every oracle's evidence alike. This is a
record-level failure account even though the storage fields are per subject.

Keep a record-level failure distinct from subject evidence. Do not replace
the intentionally shared completed-process observation union with invented
per-subject observations: REQ-result-record attaches that union to every subject.
Preserve the union and independently name the record-wide failure and any
genuinely subject-specific diagnostic.

Lands: tooling-redesign 5.4.
