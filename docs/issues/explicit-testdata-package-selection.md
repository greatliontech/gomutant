# Explicit fixture package selection cannot load a testdata package

Lands: cross-tool train chunk 188

VMM keeps a guest-helper command and its host-side unit tests in
`testdata/linux-init`, in the existing module rather than a nested module.
Explicit `go test ./testdata/linux-init` selection works. A module-root
gomutant ephemeral invocation cannot select that package by its directory or
complete import path, while using the package directory as the invocation root
works. This is distinct from tagged, browser, or subprocess-built oracles.

## Observed Selection Boundary

The field report is from gomutant v0.55.0, revision `9e60201488cf`, with
gofresh v0.98.0. Pulling to `87b409d` before filing changed documentation only.
No VMM measurement was rerun for this report.

`internal/engine/engine.go` loads `./...`. Go's wildcard excludes `testdata`
packages; `Tree.resolveTestPackage` in `ephemeral.go` resolves directory and
import-path spellings only against the already-loaded package set. Its refusal
messages are `holds no loaded package` and `is not a loaded package import
path`, respectively. An explicit oracle selector does not load the requested
package.

The concrete edit is removal of the ACPI header checksum check in
`testdata/linux-init/main.go`, decided by `TestValidateACPITableHeaderFields`:

```json
{"edits":[{"file":"testdata/linux-init/main.go","old_string":"if sum != 0 {","new_string":"if false {"}]}
```

Historical module-root invocation shape, with that batch:

```sh
gomutant ephemeral --test-pkg ./testdata/linux-init --batch /path/to/root-batch.json --run '^TestValidateACPITableHeaderFields$' --runs 2 --oracle-timeout 10s
```

Using `--test-pkg github.com/thegrumpylion/vmm/testdata/linux-init` has the same
loaded-set limitation. Changing to `--dir <vmm>/testdata/linux-init --test-pkg .`
and making the batch path `main.go` produced two consecutive checksum-mutant
kills in the original package. It did not copy the helper or add a module.
That package-local workaround establishes neither module-root discovery nor
fixture-local campaign freshness.

## Required Resolution

Reproduce with a reduced, tool-owned module containing an explicitly selected
package under `testdata`, without running new measurements in VMM while its
upstream gate is closed. Settle and document the explicit-loading behavior for
oracle directory/import selectors and explicit campaign targets, separately
from the contract that package filters merely filter discovered targets.

An explicitly selected fixture needs a supported path through loading, test
selection, mutation application and attribution without source duplication or
an artificial module boundary. Preserve tree-escape, build-selection and
no-test-selection refusals. Add deciding host-side regressions for both package
spellings and establish the persisted campaign/freshness boundary independently
of ephemeral success. If a surface intentionally remains unsupported, state
that boundary and its remedy explicitly rather than treating it as measured.

`workspace_test.go` covers directory shorthand for loaded packages, and
`ephemeral_validation_test.go` covers unloaded-package refusals; neither
supplies this explicit fixture-loading path. The separate
[external-oracle issue](external-oracle-for-tagged-and-subprocess-targets.md)
does not cover it.
