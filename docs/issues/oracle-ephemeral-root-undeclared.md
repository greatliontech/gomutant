# Tool-minted oracle TMPDIR is never declared, so temp-touching oracles are unverifiable

**Lands: cross-tool train chunk 85.**

Field report (consumer campaign: subshuffler, ~9 packages, heavy
`testing.TempDir` oracle use; verified against v0.32.1/gofresh v0.55.0
and HEAD v0.34.0/gofresh v0.64.4, Linux, go1.26.5; mechanism re-verified
against HEAD source at filing).

## Fault

`oracleScratch` (internal/engine/run.go:1126) mints one out-of-module
`TMPDIR` per oracle process tree (`os.MkdirTemp("", "gomutant-oracle-*")`)
and sweeps it with mode restoration — but ingest never declares it:
`processObservationContext` calls `runtimeinput.FromTestLogEnv` with only
`WithCompletedProcess` and `WithBracket` (run.go:644), and
`captureOracleBracket` adds only
`WithBracketExcludedPaths(".git", ".stipulator", ".gomutant")` (run.go:602).
So the scratch root the tool itself minted classifies as an uncovered
runtime input, every `testing.TempDir`-touching oracle yields
machine-local runtime-unverifiable evidence, findings in such oracle
groups are re-measured every run, and their survivors bucket
`unstable-oracle` instead of `never-executed`/`executed-and-passed`.
Reported scale: 719 of ~1400 candidates' survivors unbucketed; survivor
triage and campaign caching unusable for filesystem-exercising projects.

Caller-side workarounds all refuse, correctly, per the runtime-inputs
spec: default `/tmp` records an external directory input; a module-local
`TMPDIR` records uncovered scratch; a `TMPDIR` under a bracket exclusion
is refused (exclusion exempts the fingerprint, not runtime-input
coverage); `--bracket-path` over a churning scratch root seals
observations by design.

## Fix seam

gofresh's admission for exactly this shape already exists:
`runtimeinput.WithEphemeralTempRoot` (REQ-inputs-ephemeral-root,
runtimeinput/runtimeinput.go:410) — absolute out-of-module temp roots
whose reads record nothing. `oracleScratch` already returns the minted
directory; thread it to `processObservationContext` and declare it on
the `FromTestLogEnv` call. The root satisfies REQ-inputs-ephemeral-root
by construction: clean absolute path, per-run, outside the module,
absent again after the sweep. The caller-declared in-module complement
(`WithScratchNamespace`, e.g. a `--scratch-namespace DIR:PATTERN`
surface) is the [oracle-scratch-namespaces](oracle-scratch-namespaces.md)
declaration surface, chunk 37 — not this seam.

## Adjacent diagnostic from the same campaign

A mutant of path-handling code under test can write into the package
directory during oracle execution, tripping the (correct, target-local)
tree-drift refusal — observed as an untracked `internal/store/…` file
aborting with the generic "tree changed under measurement"
(driftabort.go:32). The first message reads as operator error although
the next run's cache self-resolves it; the decision line should name the
mutant-execution provenance when the drift appears only under
measurement.

## Repro

Any module whose tests use `t.TempDir()`:
`gomutant run --symbol <pkg>.<TempTouchingSymbol> --force` → guidance
names the temp-touching tests; targetEvidence carries
`observationObservable: false` with the bracket-coverage reason
(`runtime input not covered by observation bracket:
/tmp/gomutant-oracle-…`).
