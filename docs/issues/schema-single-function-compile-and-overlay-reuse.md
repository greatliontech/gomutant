# Mutant schema single-function SSA compile blowup, per-oracle recompilation, and `PWD` tree pollution

Lands: Next `gomutant` engine pass (alongside or immediately after `gofresh` `docs/plans/plan.md` Chunk B.3).

## Evidence and diagnosis

During a staged campaign in `git-go` (`gomutant run --staged --changed HEAD` across `stores/reference/{files,reftable,gitdir}`, 241 targets / 20,858 candidates), the campaign stalled for 43+ minutes on a single target (`230/241`, `stores/reference/reftable.(*Store).Transfer`, ~585 lines, 925 candidates, 3 nested closures) after 1h07m on the preceding 229 targets, with even larger `stores/reference/files` methods (`Transfer`, `ApplyOverlayBatch`, `executeSetStep`) still queued behind it.

Live inspection of `/tmp/gomutant-schemas-*/` and `internal/engine/{schema,schema_exec,run}.go` isolated four concrete issues:

1. **$O(L \times M)$ inline function-body duplication triggers superlinear `cmd/compile` SSA blowup (`internal/engine/schema.go`).**
   `extractFuncBodyInner`, `buildSwitchBody`, and `attachSymbolSchema` clone the entire $L$-line function body (`fd.Body`) for every candidate $1 \dots M$ as an inline `case "<mutantID>": <full body>` arm inside a single `switch _gomutantActive_<hash>()` in the target function, and `MergeCandidateSchemas` (`run.go` `executeWindow`) merges all window pieces per package.
   - On `stores/reference/reftable/store.go` (1,421 lines / 45 KiB), `replacement-1.go` expanded to **540,607 lines (17 MiB)** inside **one Go function**.
   - Go's SSA compiler (`cse`, `prove`, `liveness`, `regalloc`, escape analysis) scales superlinearly in basic blocks and locals *per function*, while `compile -c=N` parallelizes *across functions*. As a result, a single `compile` worker pinned 1 CPU core at 100% for **27.5 minutes** (peaking at ~1.9 GiB RSS), while `spliceSchemaFile` (`format.Source`) and mutex-serialized `schemaTypecheckFile` (`schemaTypecheckMu`) also parsed, formatted, and typechecked the 540k-line AST.

2. **Duplicate multi-minute recompilation of the mutated package across downstream `testPkg` oracles (`internal/engine/schema_exec.go`).**
   `SchemaCache.getOrCompile` keys on `dir + "\x00" + schemaKey + "\x00" + testPkg`, and `compileSchemaEntry` writes `replacement-*.go` + `overlay.json` into a fresh random `os.MkdirTemp(baseDir, "pkg-*")` per `testPkg`. Because Go's build cache hashes overlay replacement paths/metadata (and concurrent `testPkg` misses run `go test -c` simultaneously), `cmd/compile` compiled `stores/reference/reftable`'s 17 MiB `replacement-1.go` **twice**: 27.5 minutes for `stores/reference/reftable.test`, followed by another 15+ minutes for dependent oracle `stores/reference/gitdir.test`.

3. **`mode=serial-full` confirmation storm and zero bank reuse on subprocess-invoking tests.**
   Oracles containing CLI interop tests that invoke `/usr/bin/git` (`exec.LookPath("git")`) repeatedly logged `unstable oracle evidence (runtime input observations could not be merged for reuse: runtimeinputs: merge input 0 moved)`, yielding `0` served targets across edits (`224 machine-local, 0 served`) and forcing `mode=serial-full` confirmation re-runs across open candidates.

4. **Mutant runs with `PWD = effectivePkgDir` can pollute the live working tree (`REQ-mut-overlay`, `internal/engine/schema_exec.go` & `internal/engine/run.go`).**
   `runSchemaBinary` and standard mutant execution set `PWD` to `effectivePkgDir` in the caller's working tree. When `gomutant` mutated an empty-path guard (`if opts.CommonDir == ""`) ahead of `filepath.Clean(opts.CommonDir)` in `stores/reference/gitdir.Open`, tests passing `CommonDir: ""` resolved `filepath.Clean("")` to `"."` and wrote `HEAD`, `logs/HEAD`, and `refs/worktree/` directly into `stores/reference/gitdir/` in the live repository, violating `REQ-mut-overlay`'s runtime working-tree purity invariant.

## Required outcome

1. **Keep per-function SSA size $O(L)$ and bound schema batch size:**
   Emit each mutant arm as a top-level helper function (`func _gomutant_<sym>_<id>(<recv, params>) (<results>) { <mutated body> }`) dispatched by a 1-line-per-case `switch` in the target function (preserving receiver/parameter semantics and named-result returns while giving `cmd/compile` linear $O(L)$ per-function SSA complexity and multi-core `-c=N` function parallelism), and enforce a maximum arm count / byte budget per schema overlay so pathological packages split cleanly across bounded schema shards instead of generating multi-megabyte source files. Avoid redundant whole-file `format.Source` / mutex-serialized `go/types` passes over merged megabyte-scale files when each piece is already validated.
2. **Share compiled mutated-package archives across `testPkg` oracles:**
   Materialize replacement files and `overlay.json` once per `(dir, schemaKey)` in a deterministic directory under `SchemaCache.baseDir` (and coordinate or pre-build the mutated package archive once before concurrent downstream `go test -c` links) so `GOCACHE` compiles the mutated package at most once regardless of how many downstream test packages exercise it.
3. **Diagnosis / containment for moving external binary inputs and `PWD` tree writes:**
   Ensure runtime-input observation merging or narrowed test selection avoids degenerating every target in a package into `mode=serial-full` when only a subset of interop tests execute external binaries, and protect `REQ-mut-overlay` runtime tree purity against relative `"."` writes when a mutant disables empty-path validation on caller-supplied directories.
