# The derived-enumeration lag gate has no clause

internal/engine/enumerationcheck.go refuses a measurement whose derived
test enumeration lags the tree ("derived test enumeration of <pkg>
lags the tree (…); reload before measuring"), matching the on-disk
test set against the enumeration under the tree's effective build
configuration — GOOS, GOARCH, CGO_ENABLED, and the -tags of GOFLAGS
from the one env snapshot. No requirement in docs/specs/execution.md
states the gate or its configuration source, so its three witnesses
(TestVerifyTestEnumerationDetectsLag,
TestVerifyTestEnumerationHonorsEffectiveBuildTags,
TestBuildMatchContextResolvesEffectiveConfig) bind to nothing. The
gate is a correctness refusal (a stale enumeration would measure
against tests the binary does not compile); its clause belongs beside
REQ-exec-observation's enumeration language, with the three tests
bound to it. The coverage is already written — chunk 246 gave the
matcher pin the effective GOOS/GOARCH/cgo assertions — and needs only
its clause and its bindings.

Lands: cross-tool train chunk 209 (gomutant's spec corrections).
