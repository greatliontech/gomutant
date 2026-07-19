# Gomutant cache artifacts appear as changed-scope residue

Lands: when changed-scope discovery reports gomutant-owned cache/config changes
separately from source residue, or ignores local cache files that cannot produce
mutation targets.

## Observed

After gomutant wrote `.gomutant/findings.json` in a consuming repository,
subsequent `gomutant run --changed HEAD` output included the findings document
as changed, untargeted residue: `not a Go source file`. Stable target documents
under `.gomutant/` and local findings/cache files are expected support artifacts;
listing them beside source/test/doc changes adds noise to every changed-scope
run.

That noise becomes worse if gomutant adopts a repo/local cache split: local cache
state should be useful for reuse, but it should not make changed-scope discovery
look like user source changed.

## Resolution

Classify gomutant-owned artifacts separately in changed-scope inspection and run
output. Stable target/config files may still be reported when they change the
effective target set, but findings/cache files should not appear as ordinary
untargeted residue. Local cache files should be outside the repo or ignored by
changed-scope discovery entirely.

The output should preserve safety: if a changed gomutant config affects target
selection or cache interpretation, name it as a configuration change; otherwise
avoid polluting the source-change residue list.
