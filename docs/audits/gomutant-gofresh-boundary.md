# gomutant and gofresh ownership audit

This audit separates mutation-testing responsibilities from source-freshness
responsibilities and assigns the findings found while reviewing both
repositories. It is a review record, not either repository's behavior contract;
the canonical contracts remain the files under each repository's `docs/specs/`.

## Reviewed baselines

| Repository | Revision | Worktree |
|---|---|---|
| `greatliontech/gomutant` | `0f389ce` | clean |
| `greatliontech/gofresh` | `b6d2792` | clean |

The audit was performed on 2026-07-10 with Go 1.26.4. Neither repository had an
active Plan. Existing issue docs were treated as already tracked and were not
duplicated here.

## Boundary decision

`gofresh` owns the freshness of one caller-named Go subject. It computes that
subject's source closure, captures code and environment guards, interprets the
runtime-input manifest supplied by the caller, applies purity assertions, and
returns `valid`, `stale`, or `unverifiable`. Applied purity must be exposed as
attributable evidence even though `gofresh` does not own its persistence. It does
not run the subject, observe its runtime inputs, choose a set of subjects, or own
a result store
(`gofresh/docs/specs/overview.md:3-9,86-103`).

`gomutant` owns mutation targets and oracles, changed-scope discovery, mutation
generation, test execution and attribution, survivor dispositions, and the
findings document. A mutation record concerns several subjects, so `gomutant`
must compose the `gofresh` fingerprint of the target and every distinct oracle
test with its own operator-set, budget, oracle-membership, survivor, and
attestation data (`gomutant/docs/specs/targeting.md:17-83`,
`gomutant/docs/specs/results.md:11-72`).

The intended freshness predicate is:

```text
finding reusable =
    target gofresh verdict is valid
    AND every distinct oracle gofresh verdict is valid
    AND target and oracle identities are unchanged
    AND applied purity evidence is attributable and current
    AND operator-set pin matches
    AND recorded budget covers the request
    AND execution timeout pin matches
```

A `stale` or `unverifiable` subject causes `gomutant` to remeasure. A
formatting-insensitive body hash can remain useful to `gomutant` for changed-body
targeting and mutant identity, but it is not a substitute for a `gofresh` source
closure.

## Responsibility matrix

| Concern | Owner | Evidence and limit |
|---|---|---|
| Per-subject source closure | `gofresh` | Includes reachable declarations, constants, types, variables, initialization, embedded inputs, and mutable dependencies (`gofresh/docs/specs/closure.md:27-98`). |
| Per-subject freshness verdict | `gofresh` | `Capture` and `Check` compare closure and applicable guards (`gofresh/gofresh.go:127-218`). |
| Multi-subject conjunction | `gomutant` | `gofresh` deliberately leaves composition to its caller (`gofresh/docs/specs/overview.md:86-103`). |
| Body canonicalization and direct body hash | `gomutant` | Used by changed-scope and the current findings pins (`gomutant/internal/engine/symbol.go:22-55`, `surface.go:40-157`). |
| Symbol naming | Shared convention, separate implementations | `gomutant` resolves full references (`gomutant/internal/engine/engine.go:5-10`, `symbol.go:80-149`); `gofresh` accepts package and function or method roots (`gofresh/gofresh.go:29-34`, `closure/tier2.go:156-240`). The convention is insufficient for same-named internal and external tests (GM-14). |
| Changed paths and changed mutation surface | `gomutant` | Git path discovery and declaration-level residue are mutation targeting, not freshness (`gomutant/internal/gitref/gitref.go:13-49`, `internal/engine/surface.go:40-130`). |
| Target and default-oracle enumeration | `gomutant` | `gofresh` fingerprints supplied subjects and never decides which tests vouch for a target (`gomutant/internal/engine/enumerate.go:14-130`). |
| Toolchain and build-configuration freshness | `gofresh` | Covers toolchain, platform, feature levels, cgo environment, `GOFLAGS`, and caller build inputs (`gofresh/docs/specs/guards.md:30-65`). |
| Mutation operator and budget pins | `gomutant` | These are domain pins outside source analysis (`gomutant/docs/specs/mutation.md:14-34`). |
| Execution-limit pins | `gomutant` | A timeout can change a mutant from an attributed timeout kill to a survivor, so the effective limit is mutation-domain evidence rather than source freshness (GM-15). |
| Test execution, isolation, and attribution | `gomutant` | `gofresh` never runs a subject (`gomutant/docs/specs/execution.md`, `gofresh/docs/specs/overview.md:7-9`). |
| Runtime-input observation and association | `gomutant` | The execution owner must collect every contributing process's testlog and associate the observations with the target/oracle record; an absent manifest asserts that no inputs were observed (`gofresh/gofresh.go:127-131`, `gofresh/docs/specs/runtime-inputs.md:25-47`). |
| Runtime-input manifest interpretation and merging | `gofresh` | `gofresh` re-hashes caller-supplied identities and applies stale or unverifiable semantics. Because one manifest parser carries process-local working-directory state, the manifest owner must define safe per-process merging for callers such as `gomutant` (GM-16; `gofresh/runtimeinput/runtimeinput.go:49-129`, `gofresh.go:163-176`). |
| Purity evaluation and applied evidence | `gofresh` | The engine accepts caller and source assertions, applies them to the verdict, and must expose the applied assertion as an attributable act (GF-07; `gofresh/docs/specs/purity.md:14-35`). |
| Purity evidence persistence | `gomutant` | The result-store owner persists the applied per-subject evidence beside the composed finding; changing a caller assertion cannot be invisible to the record. |
| Dirty-recording provenance and baseline policy | `gomutant` | The caller inspects capture-time commit state, persists the dirty mark, and bars that record as a baseline while retaining working-tree reuse. `gofresh` owns manifest-path interpretation and supplies the uncommitted-input query (GM-17; `gofresh/docs/specs/runtime-inputs.md:19-21,49-53`, `runtimeinput/dirty.go:13-34`). |
| Findings persistence and concurrency | `gomutant` | `gofresh` exposes fingerprint data and leaves storage to its caller (`gomutant/findings.go`, `gofresh/docs/specs/overview.md:81-84`). |

`gomutant` does not currently depend on `gofresh`. Its direct body hashes and
`GOVERSION GOOS/GOARCH` string are therefore a parallel, weaker freshness
implementation (`gomutant/run.go:101-126`, `findings.go:150-226`,
`internal/engine/engine.go:120-139`). The dependency-closure finding below should
be fixed by integrating the two contracts, not by adding another closure walker
to `gomutant`.

## gomutant audit findings

| ID | Severity | Owner | Finding |
|---|---|---|---|
| GM-01 | High | Shared seam | Findings pin only the target and directly named test bodies. A helper, constant, initializer, mutable dependency, build configuration, or observed runtime input can move without staling the record. `gofresh` owns each subject's closure, guards, and manifest interpretation; `gomutant` owns runtime observation plus capturing, composing, and persisting that evidence (`gomutant/run.go:101-126`, `findings.go:150-169`; `gofresh/docs/specs/overview.md:86-103`, `gofresh/docs/specs/runtime-inputs.md:23-47`). |
| GM-02 | High | `gomutant` | `canonText` applies NFC and `strings.Fields` to raw source, including literal contents. Behaviorally different literals such as `"a  b"` and `"a b"` can share a hash, allowing a stale cache hit and omission from changed scope (`gomutant/internal/engine/symbol.go:42-55`, `surface.go:94,156`). `gofresh` hashes exact declaration bytes and does not share this collision (`gofresh/closure/tier2.go:1257-1300`). |
| GM-03 | High | `gomutant` | Build overlays do not provide runtime isolation. Oracle tests still run from the real source tree and share files, ports, temporary names, inherited configuration, and external system resources; concurrent mutants can perturb one another (`gomutant/internal/engine/run.go:98-129`). Direct-import detection for Rapid does not enforce the general tree-purity contract. |
| GM-04 | High | `gomutant` | Timing out `go test` kills the direct Go process but not its test-binary descendants. The review repeatedly left two `lib.test -test.run=^TestGuarded$` processes alive until explicitly terminated (`gomutant/internal/engine/run.go:114-136`). |
| GM-14 | High | Shared seam | The subject convention cannot distinguish same-named internal and external tests. `gomutant` maps both to `p.TestSame` and deduplicates them, while `gofresh` rejects the two roots as ambiguous; one test body can therefore be unpinned or the integration cannot fingerprint the oracle (`gomutant/internal/engine/enumerate.go:50-68`, `symbol.go:92-100`; `gofresh/closure/tier2.go:182-240`). The shared identity contract needs a package-variant discriminator. |
| GM-15 | High | `gomutant` | The effective per-mutant timeout is not recorded or compared. A mutant that sleeps for two seconds is an attributed timeout kill under a one-second run, but the same cached finding is served for a later three-second request under which it survives (`gomutant/run.go:15-20,124-126,206`, `findings.go:42-59,150-169`). The results contract must add timeout to the record and stale predicates. |
| GM-05 | Medium | `gomutant` | Explicit oracle symbols are hashable functions but are not checked with the runnable-test predicate. `example.com/fixture/lib.Testhelper` resolves, `go test` runs zero tests successfully, and every compiling mutant is reported as a survivor (`gomutant/run.go:114-122`, `internal/engine/run.go:131-134`, `internal/engine/enumerate.go:87-130`). |
| GM-06 | Medium | `gomutant` | Ephemeral execution accepts a readable file outside the loaded tree or absent from the selected build, so an unapplied overlay can be reported as a survivor. `testPkg` is also passed in a Go-option position without validation, so values beginning with `-run` or `-exec` are parsed as `go test` options (`gomutant/ephemeral.go:40-78`, `internal/engine/run.go:176-196`). |
| GM-07 | Medium | `gomutant` | The operator grammar omits applicable sites, including boolean `for` condition negation, compiling control-statement deletions, part of boolean-operand forcing, valid large `uint64` increments, and zero returns for named basic types (`gomutant/internal/engine/mutants.go:92-233,274-304`). |
| GM-08 | Medium | `gomutant` | A forced same-symbol run can overwrite an attestation added while it was running. The run carries attestations from its original prior snapshot; the locked update rereads the document, but `MergeFindings` replaces that symbol with the stale result (`gomutant/run.go:255-272`, `internal/mcpserver/server.go:163-190`, `findings.go:229-253`). |
| GM-09 | Medium | `gomutant` | Oracle freshness is not set equality. Prior `{A,B}` and current `{A,A}` have equal lengths and pass because the prior map contains `A`; removal of `B` is hidden by the duplicate (`gomutant/findings.go:153-169`). |
| GM-10 | Medium | `gomutant` | Findings updates truncate and rewrite the only document. A crash, short write, or unlocked concurrent read can destroy or observe partial JSON (`gomutant/findings.go:256-305`). |
| GM-11 | Medium | `gomutant` | The specification corpus does not compile: `REQ-exec-ephemeral`, `REQ-mcp-ephemeral-edits`, and `REQ-target-changed` each contain two normative keywords (`gomutant/docs/specs/execution.md:31-43`, `mcp.md:23-30`, `targeting.md:58-74`). The coverage gate cannot run. |
| GM-16 | Medium | Shared seam | One target is measured by many mutant and baseline test processes, which may observe different files or environment variables. Persisting one process's manifest omits other behavior-affecting inputs, while concatenating raw logs is unsound because working-directory state is process-local. `gomutant` must identify all contributing logs; `gofresh` must provide a merge over independently parsed manifests that preserves explicit-empty versus absent semantics (`gomutant/run.go:194-272`; `gofresh/runtimeinput/runtimeinput.go:49-129`, `gofresh/docs/specs/runtime-inputs.md:25-47`). |
| GM-17 | Medium | Shared seam | A module-local observed input absent from the recorded commit makes a recording dirty and ineligible as a baseline, but the findings document has no provenance or dirty evidence. `gofresh` can identify the condition; `gomutant` must capture and persist it and apply the baseline policy without rejecting ordinary working-tree reuse (`gomutant/findings.go:42-59`; `gofresh/runtimeinput/dirty.go:13-34`, `gofresh/docs/specs/runtime-inputs.md:49-53`). |
| GM-12 | Low | `gomutant` | `strings.Count` counts non-overlapping occurrences. In `"aaa"`, the exact match `"aa"` has valid starts at offsets 0 and 1 but is accepted as unique, so the edit measures a guessed location (`gomutant/ephemeral.go:107-125`). This is distinct from the specified behavior that separate edits apply sequentially. |
| GM-13 | Low | `gomutant` | Unconditionally trimming `_test` corrupts legitimate production import paths ending in that suffix (`gomutant/internal/engine/symbol.go:91-113`, `enumerate.go:20-54`, `surface.go:64-66`). `gofresh` uses `packages.Package.ForTest` and does not share this defect (`gofresh/closure/tier2.go:52-56,114-129`). |

The existing `gomutant` issue docs already track disposition loss after a file
rename and findings retained for deleted symbols. They remain separate from this
audit.

## gofresh audit findings

| ID | Severity | Owner | Finding |
|---|---|---|---|
| GF-01 | High | `gofresh` | Caller build inputs are included in the build-config digest but are not applied to source-dependent loads: `packages.Load`, `go list`, or purity scanning. Capturing and checking under the same `WithBuildInputs("-tags=special")` can analyze the default file set both times and return `valid` after the selected `special` source changes; a default-only purity directive can also be applied to an impure special build (`gofresh/gofresh.go:77-84`, `guard/guard.go:161-204`, `closure/tier2.go:52-69`, `closure/closure.go:723-743`, `purity.go:23-33`). Build-selection inputs must shape every source load as well as the guard. |
| GF-02 | High | `gofresh` | `Check` promises to recompute against the current tree, but one `Engine` permanently caches loaded SSA, `go list` results, and guard values by package or directory. A source, `GOFLAGS`, toolchain, or other guarded change after an earlier `Capture` or `Check` can therefore return `valid` from stale in-memory state (`gofresh/gofresh.go:64-72,147-176`, `closure/tier2.go:39-49`, `closure/closure.go:39-49,723-743`, `guard/guard.go:104-130`). The API neither enforces an immutable snapshot nor offers invalidation. |
| GF-04 | High | `gofresh` | The purity scanner accepts `// gofresh:pure` because it trims whitespace, although the contract and comment require the exact directive `//gofresh:pure`. A spaced ordinary comment on a subject reaching `os.Open` can suppress unverifiability and produce the forbidden false-valid without carrying the specified directive (`gofresh/docs/specs/purity.md:26-34`, `overview.md:59-68`; `gofresh/purity.go:64-76`). |
| GF-03 | Medium | `gofresh` | `guard.Capture` always gathers the measurement-only machine fingerprint. On non-Linux systems `gatherFacts` always errors, so even a `CodeResult`, which never compares the machine guard, cannot be captured or checked (`gofresh/guard/guard.go:47-77,80-101`, `guard/machine_other.go:1-12`). |
| GF-05 | Medium | `gofresh` | Closure resolution currently exposes promoted methods under the embedding type, but purity scanning records only the declaring receiver. A `//gofresh:pure` directive on `Inner.M` is not honored when the same function is requested as `Outer.M` (`gofresh/closure/tier2.go:159-240`, `gofresh/purity.go:47-61`). The subject contract must either admit promoted aliases and canonicalize purity to the declaration, or reject those aliases consistently. |
| GF-07 | Medium | `gofresh` | Purity changes the same fingerprint from `unverifiable` to `valid`, but `Fingerprint` and `Verdict` expose neither the applied assertion nor its attribution. A caller cannot persist the explicit act required by the purity contract, and a global assertion is absent from the evidence (`gofresh/gofresh.go:36-46,86-95,176`, `gofresh/docs/specs/purity.md:32-35`). |
| GF-06 | Low | `gofresh` | `Fingerprint.RuntimeInputs` embeds a gofresh-owned, versioned base64/JSON manifest across restarts, while the spec says fingerprints carry no wire format and does not define this nested encoding (`gofresh/gofresh.go:36-46`, `runtimeinput/runtimeinput.go:408-447`, `docs/specs/overview.md:81-84`). The nested compatibility boundary needs an explicit contract or a structured-data API. |

The existing `gofresh` issue doc tracks closure-analysis cost amortization. It is
an execution-cost concern, not an ownership transfer: per-subject closure analysis
remains `gofresh`'s responsibility.

## Required contract decisions

Several corrections change observable identities or persisted evidence and require
the owning specs to be amended before implementation:

- `gomutant` results must define persisted per-subject `gofresh` closure, guard,
  runtime-manifest, and purity evidence; conjunction semantics; and whether exact
  closure hashing deliberately remeasures formatting-only source changes (GM-01,
  GF-07).
- `gomutant` must add effective timeout to the findings record and stale predicate
  (GM-15).
- The cross-repository subject convention must define package-variant identity for
  same-named internal and external tests (GM-14).
- `gofresh` must define whether promoted method aliases are subjects and, if so,
  how they canonicalize to declaration-level purity (GF-05).
- `gofresh` must either contract the nested runtime-input encoding and merge
  semantics or expose structured manifest data for caller-owned serialization
  (GF-06, GM-16).
- `gomutant` must define dirty-recording provenance and baseline eligibility while
  preserving the working-tree reuse allowed by `gofresh` (GM-17).

## Cross-repository conformance cases

The boundary needs shared conformance cases even though the implementations remain
separate. At minimum, integration tests should cover:

- target and oracle helpers, constants, initializers, embedded files, and mutable
  dependencies changing while direct function bodies remain unchanged;
- literal whitespace and Unicode changes versus formatting-only source changes;
- legitimate production import paths ending in `_test` and internal/external tests
  with the same top-level name;
- build tags, `-race`, `GOEXPERIMENT`, `GOFLAGS`, PGO inputs, and cross-platform
  toolchains;
- promoted methods, generic receivers, bodyless declarations, and workspace
  members;
- duplicate oracle identities and oracle membership changes;
- per-process and per-oracle file and environment observations, including empty,
  absent, merged, and unverifiable runtime-input manifests;
- source and caller purity assertions, attribution changes, gitignored observed
  inputs, and dirty-recording baseline eligibility;
- reuse of a `gofresh.Engine` before and after source or environment changes.

## Verification performed

The `gomutant` review ran its ordinary and race-enabled suites, vet, staticcheck,
formatting, and module-tidiness checks. Those checks passed, apart from the
separately reported leaked test binaries. The `gomutant` Stipulator compile and
coverage gates failed on GM-11.

The `gofresh` review ran its full Go test suite, CI-shaped race suite, vet,
formatting, module-tidiness check, and Stipulator compile successfully. Staticcheck
also reported pre-existing fixture-intentional unused symbols, a `runtime.GOROOT`
deprecation in `closure/tier2.go`, and an identical-expression warning in
`guard/guard_test.go`; these are not caused by the audit change. The review
inspected the canonical specs, closure loader, guard capture, purity scanner,
runtime-input encoding, CI configuration, current issue, and relevant extraction
history.
