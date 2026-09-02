# The CLI batch form's wrapper shape is undiscoverable

Consumer report (bldc, 2026-08-30): `gomutant ephemeral --batch
<file>` refuses a top-level JSON array with "parse edit batch:
expected object", while the guidance's batch line describes the batch
as "atomic file-scoped exact-match edits ({file, old_string,
new_string})" — the required wrapper (`{"edits": [...]}`,
editbatch.go ParseEditBatch) appears in neither the guidance text nor
the refusal. A caller reading the guidance writes the array.

Ask: name the wrapper in the guidance's `batch_edits` line and in the
refusal ("expected an object with an edits array"), or accept the bare
array as the batch. Reproduction: `gomutant ephemeral --batch f.json`
with `[{"file":…,"old_string":…,"new_string":…}]`.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
