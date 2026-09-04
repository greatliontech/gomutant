# Read verbs open the findings document's store twice

The read-only MCP verbs (findings, explain, prune, retarget, attest)
each open the store for the findings document, and several also load
the document through a second store opened for the same path — two
opens, two reads of the same document per call. The run verb's
preparation value already collapses its opens into one; the read verbs
want the same collapse: one open-and-load helper returning the store
and the prior findings, every read verb driving it.

Invariants preserved: the document is read once per call; the store's
layer derivation (module root, exemptions) is unchanged; the served
prior is the same value each verb reads today.

Lands: cross-tool train chunk 156.4 (the batched view set threads one
store read through every freshness consumer).
