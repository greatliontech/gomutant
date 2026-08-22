# Coverage-guided oracle ordering — run probable killers first

Field report (pando agent, 2026-08-22): pager targets ran the whole
derived 133-test suite per mutant (~6s x 4.6k candidates,
multi-hour), while the execution buckets the tool already records
(never-executed vs executed-and-passed) know which tests actually
reach the mutated symbol. Ordering each mutant's oracle run to
execute the symbol-reaching tests first with -failfast — the rest
only when none killed — cuts the common kill case to a fraction
without weakening the verdict (a survivor still runs everything; a
kill's confirmation still re-runs per the stride discipline).

The ordering must not corrupt attribution: REQ-exec-attribution's
killer classes and the derived-oracle identity (the full set remains
the oracle of record; ordering is an execution schedule, never a
narrowing). Interaction with the serial-confirmation stride and the
per-package baseline probes to be designed against execution.md.

Lands: user decision.
