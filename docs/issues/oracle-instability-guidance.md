# No guidance when package-derived oracles include unstable tests

Lands: when gomutant identifies unstable oracle tests and suggests or emits an
explicit target oracle that keeps reusable measurements stable.

## Observed

A consuming repository needed a PTY-backed test to prove that terminal detection
accepts real terminals and rejects ordinary character devices. That test is
load-bearing for the `isTerminal` helper, but it allocates runtime identities
that are not stable enough for broad package-derived mutation oracles.

When package-derived oracles included the PTY test for unrelated `cmd/stash`
targets, findings became stale or unverifiable even though those targets did not
need that terminal-specific oracle. The developer solved this by hand-authoring
explicit target files for `main` and `promptWriter`, leaving the PTY test to be
used only by a focused manual probe for `isTerminal`.

## Resolution

When a package-derived oracle makes a finding unverifiable because of one test's
runtime observations, gomutant should report the test and target pair in a way
that points to oracle narrowing. A useful first step is a diagnostic such as:

`target X uses package-derived oracle; test Y produced unstable runtime input Z;
rerun with an explicit oracle excluding Y if Y does not vouch for X`.

A stronger implementation could emit a candidate targets document that preserves
the current target symbols and replaces the broad package-derived oracle with
the stable tests that actually killed mutants, leaving the unstable test for
the symbols it directly vouches for.
