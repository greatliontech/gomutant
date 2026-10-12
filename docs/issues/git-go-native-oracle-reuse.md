# git-go native-oracle observation and reuse refusals

Lands: Gofresh docs/plans/tooling-redesign.md 5.4, with the cost and precision
questions at 7.7/7.8 and the diagnostic account at 8.3. These are generic tooling
obligations, not authorization to implement git-go features or rerun its campaign.

## Observed consumer case

git-go's first object-value implementation combines pure external-package tests
with a separately selected `gitgo_oracle` suite. Native comparisons use an
explicitly pinned Git executable, a closed process environment, and owned scratch
outside the source repository. The harness also exercises child-process failure,
resource limits and cleanup. No purity or dynamic-state vouches were supplied.

Installed producer: Gomutant `v0.64.4`, revision
`2172a8b42bb01d7be5bee23bc811ca13e4e4ad02`, findings version 16 and
`gomutant/full-oracle@2`, built with `go1.27.2-dst.15` and Gofresh `v0.115.1`.
The consumer uses that normal compiler without the `dst` build tag.

Run `989784171e8a482d` requested the complete package oracles under the native
selection and reported 45 measured targets, 661 generated candidates, 506 kills,
25 survivors and 130 discards. All 25 surviving mutations were subsequently
given explicit semantic-equivalence dispositions; zero remain open. These are
producing-run observations, not a claim that an execution-policy label alone
establishes complete oracle admission. Reconcile them with
[the execution-policy admission contract](../specs/results.md)
before granting authority that the historical record does not establish.

Every record remained machine-local because the consumer worktree was genuinely
uncommitted. That placement is expected and distinct from the reuse refusals:

- `runtime inputs: target: external directory input: /tmp`.
- `package scan: reaches testing.Context (test runtime configuration)`.
- Open interface/callback analysis, including `testing.TB.Context`, `testing.TB.Fatal`,
  `os.FileInfo.IsDir`, and the explicitly supplied process-transcript runner.
- `observation analysis unavailable: subject Compare not found in
  github.com/greatliontech/git-go/object`, and corresponding messages for ordinary
  exported value methods/functions that the campaign resolved and measured.
- Some killed candidates lack completed observation finalization. A mutated
  process failing or panicking is distinct from missing reuse authority; it is
  not automatically a consumer flake or grounds to discard an attributed kill.

The run rendered survivors as `unstable-oracle`. No consumer test flakiness was
established by that label. The measured whole runs and separate direct/race tests
completed; evidence applicability and retention remain separate questions.

## Invocation and recovery

The observed command, from git-go, was:

```sh
GITGO_GIT=/tmp/opencode/git-go-oracle-2.56.0/git \
  mlock run gomutant run --changed HEAD --tag gitgo_oracle --budget 0 --jobs 2 \
  --bracket-path /tmp/opencode/git-go-oracle-2.56.0/git \
  --bracket-path /usr/lib/libc.so.6 --bracket-path /usr/lib/libz.so.1 --timeout 20m
```

The binary was built from Git's `v2.56.0` tag with zlib 1.3.2 and loaded glibc
2.44. Paths above identify this machine's observation, not portable policy to
copy to another host. Retrieve the recorded rows in git-go with
`gomutant findings --json`; locate the consumer implementation through
`git log --all -- object/id.go internal/testgit/git.go oracle/id_test.go` once
the source checkpoint is committed. The named run and its dispositions live
in Gomutant's machine-local layer, not in an empty repository findings document.

## Required derivation

Reproduce after the current admission repair and consumer dependency adoption,
using a clean committed consumer and independently established named baseline
outcomes. Separate:

1. Expected dirty-source placement from any evidence fault also rendered as dirty.
2. Actual behavioral dependencies from owned scratch and absence observations.
3. Legitimate testing/callback conservatism from missing subject-resolution or
   observation-view integration.
4. Fresh execution outcomes, mutant attribution and proof sufficient for reuse.

Explain a narrow supported remedy for eligible evidence, and state the intended
non-reusable boundary where no such proof exists. Keep the full required oracles,
external fixture isolation and caller-owned dependencies. Do not bracket the
shared `/tmp` tree, move fixtures into the source repository, drop native tests,
or introduce blanket purity assertions merely to obtain reuse.

The [VMM refusal report](vmm-finding-admission-refusals.md) overlaps the testing
receiver/callback precision questions; the
[evidence-fault reporting issue](evidence-fault-rendered-as-dirty-provenance.md)
overlaps attribution of placement reasons. This report records git-go's
external-scratch and resolved-but-unavailable-subject observations and their
invocation. It does not commission another Gofresh implementation inside Gomutant.
