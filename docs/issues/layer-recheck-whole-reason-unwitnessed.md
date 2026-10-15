# Establish a discriminating witness for the whole-evidence recheck

Lands: tooling-redesign 5.4.

The provenance stamp rechecks an unresolvable external runtime identity before
treating it as unchanged. `stampProvenance` compares the resulting state,
reason and digest as a whole under REQ-result-layers. The need for a fixture
that independently discriminates each compared field remains.

An earlier proposed fixture relinked an in-tree symlink between two existing
external directories and expected the whole-reason comparison to catch it.
That proposal does not establish reachability: `repositoryState.placement`
in `provenance.go`
classifies an existing resolvable external target as `placedOutside`, while
the relevant recheck admits `placedOutsideMissing` and `placedUnknown`.
A git-visible symlink edit can also dirty the record independently of the
comparison. A historical surviving clause-only probe is not proof that the
proposed fixture reaches this arm, nor fresh verification of the current tree.

Construct a missing/unknown-placement case at the actual validation boundary,
show that it reaches the recheck, and mutate only the comparison under test.
Establish the unchanged control's actual placement independently; do not assume
that an external input belongs in the repo layer. Either witness a load-bearing
whole-state difference or record the demonstrated equivalence of an unreachable
or redundant arm. A diagnostic-clause split alone supplies no such result.

Secondary constituent: tooling-redesign 9.3 retains the deciding regression
and its independent mutation evidence after the producing boundary is migrated.
