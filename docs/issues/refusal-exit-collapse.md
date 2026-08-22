# One refusal exit — the hand-picked subsets keep breeding defects

run.go's target-refusal exits (the drift skip, the serve-check
refusal, the shaped-serve refusal, the staged-drift refusal, the
vanished-file and shaped-drift refusals) each hand-pick a subset of
{refuseTarget, decisions[i], the inline opts.Decision emit,
residue()} and their Action vocabulary is inconsistent (the two
"refused" outliers at the serve exits; every other exit says
"skipped"). The review of the drift-guard change set demonstrated the
breeding pattern twice in one round: the vanished-file exit's inline
emit duplicated the decision line out of order (an
REQ-exec-run-status violation, caught H-grade), and the shaped path
missed the refusal registry entirely (spec-code disagreement on the
amended universal-drift clause). Collapse to one
refuse(symbol, reason) exit owning the registry append, the decision
record, and the residue suffix; the vocabulary outliers die with it.

Lands: with the next change to run.go's refusal surface (the exits
are load-bearing in every campaign; the collapse wants its own
reviewed change set, not a rider).
