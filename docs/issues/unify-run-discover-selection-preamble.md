# run and discover duplicate the ~45-line target-selection preamble

Both MCP handlers repeat the same selection machinery: the
more-than-one-forms refusal (targets_path / targets_json / changed),
the four-way source dispatch, the empty-selection filter skip, and the
FilterTargetsContext call. The zero-target note discrimination is
already shared (selectionEmptiedNote), but the preamble around it is
two copies that must be edited in lockstep — the wrong-emptier defect
landed in both and was fixed in both, which is the maintenance shape
this filing exists to end.

Collapse sketch: one selectTargets(ctx, tree, in) (targets, wholeTree,
note-inputs, error) helper consumed by both handlers; the handlers keep
only their verb-specific consumption. Invariants preserved: form
exclusivity, filter-skip-on-empty, FilterTargetsContext's own no-match
refusal, wholeTree false under filters.

Lands: with cross-tool train chunk 128 (gomutant serve carve-out
consolidation)
