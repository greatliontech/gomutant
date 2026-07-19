# CLI --targets help says "JSON targets document" but the value is a path

Lands: 10 of the active hot-loop-ux plan (output and remediation audit).

## Observed

`gomutant run --help` describes `--targets` as "JSON targets document;
overrides discovery". Passing the document itself — `--targets "$(cat
export.json)"` — fails with `gomutant: no such file or directory`: the
value is treated as a path. The MCP surface distinguishes
`targets_path` and `targets_json`; the CLI flag is the path form with
document-shaped help. Live instance: the first stipulator-export feed
into gomutant failed on exactly this, succeeding on retry with the
path.

## Resolution

Align the help with the semantics ("path to a JSON targets
document"), or accept both forms the way many tools do (`@file` vs
inline, or sniff a leading `{`). The error could also name the fix:
"--targets expects a file path; got what looks like inline JSON".
