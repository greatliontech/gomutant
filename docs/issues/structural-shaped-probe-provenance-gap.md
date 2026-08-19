# Structural shaped targets: probe content outside the provenance and re-observation nets

A structural shaped target's serve pin is the t0 probe digest
(`shapedCandidates`) compared against `rec.BodyHash` — both pre-move
values — while `shapedProbedFiles` contributes probed files to the
dirty judgment only for `Manual` shapes. Structural probe content
therefore enters neither the serve's view re-observation (unless the
oracle's static closure happens to reach it) nor `pathsDirty`. If the
probed packages sit outside the oracle's static closure AND outside
its runtime-input manifest, a mid-run committed change to them could
serve a stale structural record stamped clean.

Reachability is NOT demonstrated: architecture-style oracles typically
read the probed tree, which puts those files in the runtime-input
manifest, and the serve precheck re-reads that manifest per pair. The
adversarial reviewer could not close the gap either way
(review of the stamp-time-provenance change set, round 2). The
mechanism pre-exists for the dirty variant and is independent of that
change set.

Candidate fix if it fires: extend `shapedProbedFiles` to contribute
structural shapes' probed packages exactly as manual recipes' probed
files, making the dirty judgment cover them; or add the probed
packages to the structural serve's evidence surface.

Lands: when a structural shaped oracle is shown (or constructed) whose
probed packages escape both its static closure and its runtime-input
manifest — a demonstrated stale-serve path — or with the next
behavioral change to shaped serve pins.
