# SIGINT loses the in-flight target's completed measurements

Field report (pando agent, 2026-08-22): an interrupt mid-target
discards every mutant already measured for that target (the document
writes per COMPLETED target — six minutes of work lost at their
interrupt). The per-target atomicity is deliberate (a partial target
cannot serve and never enters the committable layer), but the
interrupt path could do better without weakening it: finish the
in-flight MUTANT, then persist the target's partial progress to the
machine-local layer as resume state (candidate index reached, kills
so far), so a resumed run re-enters the target at its prefix instead
of from zero. Design must keep the resume honest against
INV-RESULT-CANDIDATE-CONSERVATION (the partial prefix re-identifies
deterministically or is discarded loudly).

Lands: user decision.
