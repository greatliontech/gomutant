# The MCP run response carries attestation sheds and carries as prose where contradictions are rows

runOut.AttestationSheds and runOut.AttestationCarried are []string —
each entry the sentence AttestationShed.Text / AttestationCarry.Text
renders — while a contradiction is a row (contradictionOut with
symbol, position, operator, killer, reason). A client that wants the shed's
position or operator parses
prose. The move is one row type per disposition on the wire beside
the sentence the CLI prints, a BREAKING wire change on the run tool's
response.

Since 246.A a carry names a closure-derivation move ("(closure
derivation <prior> -> <current>)") — a machine-relevant discriminator
between pins moved under a new derivation and a source move — so a
structured reader must parse it out of the prose today; the CLI's
--json face carries it as its own field.

The current `runOut` still exposes these dispositions as strings. Publish
structured rows with the full reason and derivation facts, preserving the CLI
sentence as a rendering and declaring the wire change. A carried equivalence
remains a disposition, not a license to reuse historical execution counts.

Secondary constituent: tooling-redesign 6.7 preserves these rows across closing
and recovery, including post-write failures.

Lands: tooling-redesign 8.3.
