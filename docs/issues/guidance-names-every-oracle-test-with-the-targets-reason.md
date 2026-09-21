# Guidance names every oracle test with the target's unverifiable reason

Lands: cross-tool train chunk 277 (the unverifiable read attributed to the subject whose observation carried it, or named the union's — the reading of Observation.Attribution at 277)

A delta run over pb's check subsystem (gomutant 004ee25) reported
each target's oracle evidence unstable and, per target, guided:
"rerun with an explicit oracle excluding
cmd/pb.TestClientSettingsNameTheirLayer,
internal/check/breaking.TestFromRef,
internal/dep.TestGenSymlinkEscapeRefused if it does not vouch for
this target". `explain` on a record listed, for every oracle test,
"runtime-unverifiable evidence for <test>: external directory input:
/ — open "/" in ".../internal/check/breaking"", the same reason under
each name.

A syscall trace of each test binary showed the read of `/` in the
breaking package's tests (go-git's `.git` detection walking up from a
temporary directory) and in the dep test (a billy filesystem rooted
at `/`), and none in the command layer's session test. The reason
printed under that test's name was the oracle group's, or the
target's, not the test's. The guidance sent the reader to a test
that reads nothing outside the tree, and the fix that stabilized the
oracles touched the two others alone.

What the record knows: whether the unverifiable read is attributed to
one subject's observation or to the group's union. What the guidance
and `explain` should say: the subject whose observation carried the
read, or that the read is the union's and no subject is singled out.
Naming every subject with one reason reads as an attribution the
evidence does not make.
