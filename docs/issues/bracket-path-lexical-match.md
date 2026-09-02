# Bracket paths match the observed spelling lexically, not the cleaned file

A test in a nested workspace module opens a root-module file as
`os.ReadFile("../../internal/boundary/nontree_pump.go")`. Declaring
that surface as the clean absolute path leaves every target of the
module planned `unverifiable: runtime input not covered by
observation bracket: <root>/internal/boundary/nontree_pump.go` —
the message itself prints the clean path — while declaring the
SAME file as `<root>/tools/platgen/../../internal/boundary/nontree_pump.go`
(the target module's directory joined with the test's own relative
spelling, un-cleaned) matches and the target plans as an ordinary
stale re-measure (v0.52.0, `run --plan --staged --changed=HEAD
--symbol <nested-module symbol> --bracket-path <spelling>`).

So the bracket is compared against the observed open's spelling
before cleaning, and a caller has to guess how each test spelled
its path. The contract the flag describes — "the surface the
oracle legitimately reads" — is a FILE: both sides should compare
after filepath.Clean (and symlink resolution where the observed
path went through one), so any spelling of one file declares it.

Lands: user decision
