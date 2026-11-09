# Evaluate Shared Module for CLI, MCP, and Process Supervision Utilities (Option 1B)

- **Status:** Open (user-authorized deferral)
- **Lands:** Defer until user triggers cross-tool CLI/MCP/process utility extraction

## Context

When `gofresh` purified its public API down to a single root package (`github.com/greatliontech/gofresh`) focused strictly on Go freshness and purity analysis, the cross-tool CLI, MCP, resident-memory, and process-supervision helpers (`guidance`, `resident`, `toolversion`, `shortgates`, `shapecorpus`, `gotool` process group supervision, and `ToolchainProvenance`) were internalized into `gomutant/internal/` (Option 1A) alongside `pew/internal/` and `stipulator/internal/`.

## Evaluation Scope (When Triggered)

Evaluate extracting the duplicated non-freshness CLI/MCP/process utilities shared across `pew`, `stipulator`, and `gomutant` into a dedicated shared utility module (Option 1B) once their per-tool surfaces have stabilized:

1. `internal/guidance` (embedded markdown guidance parser and CLI/MCP help projection)
2. `internal/resident` (Linux `/proc` process-tree RSS/PSS sampling and soft `GOMEMLIMIT` ceiling installation)
3. `internal/toolversion` (VCS build-info version string formatting)
4. `internal/shortgates` and `internal/shapecorpus` (test-suite gate discipline and Go language-shape canary fixtures)
5. `internal/gotool` (process-group containment, `go env -json` snapshot caching, and command preparation)
6. `internal/trace` (zero-disabled-cost structured diagnostic tracing interleaved with `gofresh` via `GOFRESH_TRACE` / `GOFRESH_TRACE_FILE`)
