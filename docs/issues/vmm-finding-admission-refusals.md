# VMM measurements remain unverifiable after successful execution

Lands: user decision

## Evidence

The VMM consumer (`github.com/thegrumpylion/vmm`, commit `7ac2e7a`) recorded
passing mutation executions with gomutant v0.55.0, revision
`9e60201488cfac663b69b8f8d69f1ebfd5257f01`, using gofresh v0.98.0 and
`go1.27.0-X:nodwarf5` with `GOEXPERIMENT=nodwarf5`. These are previously
observed results, not a reproduction performed while filing this report.
Gomutant was pulled to `87b409d` before filing; that pull changed issue
documentation only and still pins gofresh v0.98.0.

The toolchain notice says the release is not listed and disables
standard-library observation admission. The shared root-cause report already
exists in gofresh:

https://github.com/greatliontech/gofresh/blob/main/docs/issues/nodwarf5-toolchain-audit-key-mismatch.md

Both audit-key lookup and baked-experiment parsing need consistent identity
handling. This issue owns gomutant adoption and the consumer-level regressions,
not another implementation of gofresh's toolchain parser.

Repository placement does not settle freshness. The consumer's recorded
findings have these judgments:

| Target, relative to the VMM module | Recorded run | Judgment reason |
|---|---|---|
| `boot/linux/x86.parseBzImage` and `boot/linux/x86.BzImage.SupportsSetupData` | `b824ba94fa8186d9` | `target: reaches os.ReadFile (file I/O)` |
| `machine/x86/acpi.BuildFADTForPowerLayout` | `095fd4827690640f` | `target: testing runtime value escapes analyzable receiver` |
| `resource.Pins.Entries` | `57dd184c1ce05a05` | `target: testing runtime value escapes analyzable receiver` |
| `resource.Pins.Equal` | `0037bfce455e6edc` | `target: testing runtime value escapes analyzable receiver` |
| `resource.Inspection.Pins` | `57dd184c1ce05a05` | `target: subject accepts caller-supplied dynamic behavior` |

The boot records contain 318 killed mutants, 47 discarded candidates and two
reasoned equivalent-survivor attestations. The three private-pin records
contain 29 killed mutants and 14 discards, without survivors. Their target
documents, `.gomutant/boot-targets.json`, `.gomutant/resource-targets.json` and
`.gomutant/acpi-targets.json`, select named runtime oracles rather than the
consumer's source-reading architecture guards.

## Reproduction Boundary

The source and recorded evidence can be inspected at the named VMM commit.
Historical invocation shapes were:

```sh
env -u VMM_UPDATE_GOLDEN gomutant run --targets .gomutant/resource-targets.json --budget 0 --jobs 4 --staged --timeout 5m
gomutant findings --judge --symbol github.com/thegrumpylion/vmm/resource.Inspection.Pins --json
```

VMM has paused measurements pending upstream resolution. Reduce the cases into
gomutant/gofresh-owned fixtures; do not use a new VMM campaign as the filing or
diagnosis step while that gate is closed.

## Required Resolution

- Adopt the repaired gofresh dependency and verify the actual built tool's
  release and experiment selection, not only its module declaration.
- Trace each residual refusal through the observed-proof and subject-level
  paths. A maximal-scan receiver-escape diagnostic is not itself proof that a
  subject's observation is inadmissible; conversely, a method containing a
  dynamic carrier is not automatically closed merely because its selected test
  uses a fixed fixture. Keep these cases separate from the audit-key mismatch.
- Add reduced regressions for demonstrated precision or consumer-integration
  faults. For a legitimate conservative refusal, identify the exact input or
  proof boundary and the supported remedy; do not demand that every witness
  become cacheable or grant blanket callback, standard-library or dynamic-state
  vouches.
- Check recorded-versus-judged reporting and the required remeasurement path
  after adoption. Previously executed kills must not be promoted into reusable
  evidence without the required proof.

The existing gofresh issue
`docs/issues/sibling-reason-families-name-their-channels.md` overlaps actionable
wording for caller-supplied behavior, but does not resolve these consumer proof
reductions. Existing VMM records are current-generation evidence; no legacy
findings migration or inheritance of old equivalence judgments is requested.

## Additional Stash Evidence And Adoption Acceptance

Stash independently observed the same audit-key mismatch with gomutant v0.55.0,
GoFresh v0.98.0, and `go1.27.0-X:nodwarf5`. Its source-derived comparison shows
space-form release/selection keys and a space-only `binaryExperiment` parser in
the dependency. Stock Go 1.27.0 is listed; the actual experiment spelling is the
missing identity, not general Go 1.27 support.

Stash's mutation executions completed, but its persisted observation dispositions
included unaudited-standard-operation refusals. This does not establish the
cause of every later dynamic-state/callback refusal. The
[measured/reuse diagnostics issue](measured-findings-reuse-diagnostics.md) owns
the separate presentation wrinkle and documents those boundaries.

The shared consumer-adoption acceptance also needs:

- a released repaired GoFresh dependency and an identifiable Gomutant release
  containing it, not only a source checkout or suppressed notice;
- consumer regressions for default/race selections and inherited/explicit
  matching experiments, retaining unlisted-release, mismatched-experiment, and
  unwalked-selection negative controls;
- observation publication/reuse for an eligible deterministic fixture without
  purity or dynamic-state vouches, while legitimate refusals remain intact;
- installed CLI and MCP process provenance matching the repaired builds.

Stash remains on Go 1.27.0 without a separate verification-toolchain pin, and all
of its measurements are blocked on the upstream fixes. Use tool-owned fixtures
for acceptance rather than a Stash canary. This shares the adoption work already
owned by this report; it does not require all VMM or Stash records to become
cacheable.
