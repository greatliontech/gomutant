# engine API: a multi-package oracle run clobbers its one testlog

`runMutantOnce` passes a single `-test.testlogfile` after multiple
`testPkgs`; each sequential test binary truncates the file, so the
ingested capture is the last binary's - header present, a completed
observation silently covering only one package's reads. Unreachable
from `Tree.Run` (groups are always single-package), so no campaign is
affected; the hazard is the exported engine API
(`RunMutantObserved`/`RunMutantObservedEnv` with multiple test
packages and observation enabled). A fix would be per-package capture
files with a merged observation, or a refusal when observation is
requested for a multi-package run.

Lands: when an engine consumer passes multiple test packages to an
observed mutant run.
