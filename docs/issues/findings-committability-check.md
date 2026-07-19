# No committability check for findings artifacts

Lands: when gomutant can report whether a findings artifact is suitable for the
repo cache and can export or refuse a portable-only version.

## Observed

After mutation runs in a consuming repository, `.gomutant/findings.json`
contained a mixture of killed candidates, attested equivalent survivors, dirty
provenance, external directory inputs, and unverifiable runtime observations.
The file was useful locally, but a code reviewer correctly flagged it as a poor
commit artifact because it encoded local execution state.

The tool had already produced all the information needed to make that judgment,
but the caller had to inspect the JSON or `gomutant_findings` output and decide
manually whether the file was safe to stage.

## Resolution

Add a command or mode that answers the artifact question directly. Examples:

- `gomutant findings --committable` reports every record that prevents the
  findings document from being a clean repo-cache artifact;
- `gomutant export --portable` writes only portable reusable records and refuses
  or reports omitted local-only records;
- MCP findings inspection returns a top-level `committable` boolean plus reasons.

The check should understand the repo/local cache split: clean content-pinned
records belong in the repo cache, while dirty or runtime-unverifiable records
belong in the local overlay.
