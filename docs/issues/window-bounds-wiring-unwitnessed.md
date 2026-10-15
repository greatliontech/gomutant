# The run's jobs-derived window bounds are unwitnessed at the wiring

The window partition's property and anchors exercise gatherWindow
directly. The bounds derived from the worker count at the run's gather
call are executed by every run-level test that leaves the window seam
unset, but discriminated by none: no fixture reaches the floor's sixty-
four candidates or the budget's executions, so neither bound ever closes
a window, and a run that derived its bounds from a wrong input, or
passed them in the wrong order, would pass every pin. The witness is a
run over a fixture yielding more candidates than the floor (sixty-four)
under one worker, asserting the first window's candidate total at the
ceiling; the fixture's size is a design of its own.

Current deterministic membership still derives from worker count, candidate
count and oracle size; removal of the coverage scheduler does not remove this
window rule. Pin the actual call-site bounds at both cliffs while making a
measured window close after its own validation/confirmation, independently of
successor preparation. Do not change membership to a timing- or execution-subset-
dependent rule to make the new closing boundary easier to implement.

Lands: tooling-redesign 6.4.
