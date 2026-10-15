# The interned tables' entries admit a duplicated key

A version 11–13 document's interned tables (the evidence, runtime-input,
and ledger tables) decode through `json.Unmarshal` into internedDocument,
which carries no duplicate-key refusal: an evidence-table entry with two
`evidence` members — two values for one fact — takes the last, where an
inline finding's duplicated key refuses the document (the refusal the
fingerprint-record change set restored for the upgraded rows). Found by
that change set's reviewer over a hand-built version-12 document; no
writer produces the shape.

Collapse: the table entries decode through the same known-object walk
the inline findings use (decodeKnownObject over each entry, then the
typed decode), so one duplicate rule covers every record shape.

The current writer is version 16; versions 14–16 use content-keyed tables,
while older readable versions retain their decoder paths. Reconcile duplicate
members in both families and in nested known objects without re-encoding away
the malformed input before admission. A writer's inability to emit the shape
does not settle decoder safety. Preserve valid siblings and history, but never
upgrade missing execution policy or native support through decoding.

Lands: tooling-redesign 9.2.
