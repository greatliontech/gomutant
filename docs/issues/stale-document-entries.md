# Findings for deleted symbols persist in the document

Lands: when document hygiene first bites in real use, or when the findings
verb next changes.

## Context

saveFindings merges fresh findings over the prior document by symbol so a
scoped run never drops the rest of the document. The flip side: a symbol
deleted from the tree keeps its finding forever — never re-served (never
targeted again), but `gomutant findings` presents its open survivors
indefinitely, and a caller failing builds on open findings has no in-tool
remedy for code that no longer exists. Not a staleness violation (nothing is
served from it), but the findings surface misleads.

## Resolution shapes

Either a whole-tree `run` prunes document entries whose symbol is absent
from the tree (a scoped run must not), or `findings` flags entries whose
symbol no longer resolves. Needs a spec sentence in results.md either way —
document hygiene is currently unspecced. On landing: implement, spec,
promote this rationale, delete this doc — git holds history.
