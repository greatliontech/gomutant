# Decision-view build aborts campaign-wide on one target's breakage

The freshness-proof path tolerates per-symbol faults (a broken package
faults its module group out of the observed union and the target skips
after one bounded retry, REQ-exec-quiescence), but the same breakage
hits two strict sites upstream first: the body-hash read
(`run.go` target loop, `"target %s: %w"` abort) and the decision-view
batch build (`"freshness: %w"` abort). A target package with a type
error — parse-clean, so the body hash can succeed — fails the decision
batch's typed load and kills every target: one target's evidence
construction aborting all, the exact shape the proof path's
target-locality discipline forbids. In the field the union's fault
tolerance is therefore exercised only by faults arising after the
decision build (transients), never by persistent breakage.

Two interlocked decisions, both the user's:

1. **Spec.** REQ-exec-quiescence's target-locality clause names only
   freshness-proof construction. Either it extends to symbol
   resolution and decision-evidence construction (making the
   campaign-wide abort a spec violation to fix, presumably with the
   same fault-map shape the union uses), or the spec names
   resolution/decision failure as an admitted campaign-wide class.
2. **Mechanism.** The decision batch (maximal captures) and the
   observed proof union are two back-to-back full observation passes
   over the identical symbol set with the same engines. One observed
   union view set could serve both roles — Capture and
   CaptureObservedBatch coexist on one view — halving the warm
   campaign's observation floor. Folding them changes decision-failure
   locality as a side effect, which is why this rides with decision 1
   rather than landing as a pure consolidation.

Decision 2's outcome also dispositions the strict/union view-build-loop
duplication in `freshness.go` (the back halves of the two constructors,
parallel except fault routing): a fold deletes or reshapes the strict
observed arm's callers; a no-fold outcome makes that duplication a
free-standing consolidation candidate again.

Lands: cross-tool train chunk 39.
