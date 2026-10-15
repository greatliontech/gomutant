# gofresh corpus pin lags the latest release (fleet sweep 2026-10-12)

The 2026-10-12 weekly fleet sweep's shape-corpus check reported this module pinned
to gofresh v0.115.1 while the latest release tag is v0.116.1 (the
remote is the authority; the local clone may lag it). Version lag is
the one drift channel the shape corpus leaves open — corpus content
drift is unrepresentable — so a consumer behind the release judges
purity, inputs and freshness with an older corpus than the one
released. The remedy is a release-before-adoption bump carrying the actual
producer and checker migration obligations, followed by clean installation.
The reported latest tag is historical, not a claim about the remote today.

The current `go.mod` still pins v0.115.1. Merely changing that version does not
complete the target/window migration or establish native outcome support for
transformed executables. Record the adopted release, compatibility decisions and
actual installed identities; keep conservative identity-only evidence where no
outcome method applies. No dependency update or sweep was run for this edit.

Lands: tooling-redesign 5.4.
