# The audit and the serial confirmation duplicate the rescore mechanism

Two post-pool serial re-score passes share identical
replace-wholesale semantics — outcome, killer, memoryDecided,
observation, incomplete replaced from the authoritative full run —
but carry the channel list and the probe-gate posture independently
(the chunk-137 review's H1 was exactly a divergence between them: the
audit initially took no gate). Collapse to one rescore mechanism
(gate acquisition, scopedWork scoping, channel replacement as one
value) that both the confirmation walk and the narrowed-survivor
audit call, making a future divergence unrepresentable. The
confirmation's stride/flip machinery stays its own; only the scored
re-run and its replacement discipline unify.

Lands: with train chunk 136 (the consolidation audit), or the next
change to either rescore path.
