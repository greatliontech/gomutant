# Evidence pins are machine-local; the findings document does not travel

Field report (bldc campaign, 2026-08-27): a 5-hour whole-tree run
committed a 415-record findings document whose evidence pins embed the
producing machine: every `runtimeInputs` entry (target and oracle) is
an absolute path rooted at the checkout
(`{"k":"abs","..." :"/home/nikolas/repos/.../testdata/..."}`),
`oracleMemoryBytes` is host-RAM-derived, and the toolchain string names
the host build. Nothing sensitive leaks — the paths stay inside the
repository — but the serve contract quietly narrows to one checkout: a
clone at a different root, or a CI runner, mismatches the pins and
silently re-measures everything the document already paid for.

Runtime-input path pins should be module-relative (the module base is
already the identity anchor elsewhere — the `abs` kind looks like the
odd one out), and host-capacity facts like `oracleMemoryBytes` should
ride a machine-profile facet outside evidence identity, so a served
verdict is keyed by what was measured, not where.

Lands: with cross-tool train chunk 133 (gofresh
docs/plans/cross-tool-train.md).
