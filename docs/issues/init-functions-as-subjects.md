# func init() bodies are excluded from mutation instead of measured

Lands: cross-tool train chunk 40.
## Context

Changed-scope and whole-tree discovery exclude `func init()` loudly (the
language keeps the identifier unreferencable, so it cannot ride the
resolver symbol grammar). The exclusion resolved a campaign-level abort,
but it leaves a real coverage gap: init bodies carry registry wiring —
a dropped registration is a classic silent fault a mutation campaign
should catch, and any test of the package executes every init, so the
oracle relationship is the package suite by construction.

## Shape

Measuring init needs an identity outside the current grammar
("<pkg>.init#<ordinal>" or file-anchored), candidate generation over a
body with no addressable declaration, and freshness-proof support for
subjects gofresh's symbol-keyed views cannot name today. The ordinal
identity is unstable under file reordering — acceptable for a
measurement key (worst case: spurious remeasure), not for a durable
findings identity; a content-anchored key may serve better.
