# The mode's decision views stay resident for the whole run

The derived oracle's freshness proofs now run per proof unit — the
targets sharing an oracle package set — as siblings of the mode's
decision views, each unit's proofs released at its last target's
terminal disposition. The decision views themselves are built over
EVERY symbol of the mode before any unit proves (the `views` pricing
line), and they stay resident for the run: each target's decision reads
them, and the run-end producer validation re-observes them after the
last commit. Measured 2026-10-05 over gomutant's own campaign (twenty
targets, 725 candidates): the process's high-water mark stood at 1.4 GB
after the decision views and before any proof.

So the unit loop's vertical is complete for the proof pass and not for
the decision: the decision's typed load and loaded programs are the one
mode-wide resident set a campaign still holds from its first target to
its last. Slicing the decision views per unit needs the run-end
validation re-shaped (it validates the views the decisions read; a
per-unit decision view is released before the run ends) — a design of
its own, beside the attach spike and the idle server's set.

Measured 2026-10-05 at 308's open: a gofresh View retains facts alone
(digests, string maps) — the Hasher that loads programs is built per
capture call and dropped after it — and the one-target run that built
views over 796 subjects sat at 0.39 GB warm after them; the 1.4 GB was
the cold views pass's working set, not retained state (the cold
one-target run returned to 0.41 GB at its end). The decision's typed
load is gomutant's Tree (every package of the module), resident for the
run by construction.

Lands: cross-tool train chunk 308 (closes with this verdict; the cold
pass's own retention is gofresh 314's).
