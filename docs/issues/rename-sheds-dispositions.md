# A file rename sheds attestations on the re-measure path

Lands: when survivor identity gains a file-independent component (e.g. a
body-relative line plus operator), or when rebase-across-rename first bites
in real use.

## Context

Survivor positions are basename-keyed (`lib.go:10:5`). Renaming a file
without touching bodies leaves every pin content-matched, so a plain run
serves the cached finding intact — but a `-force` or budget-widening
re-measure produces survivors at the new basename while carried attestations
rebase to the old one; the open-membership filter finds no match and every
disposition is shed. That is drift from an edit outside the body shedding a
disposition — the counterexample REQ-attest-survivor's rebase clause exists
to prevent, reachable only through the rename+re-measure combination.

Inherited shape (stipulator's kill-sheets key positions identically); the
verdict arithmetic is unaffected — kills and survivors stay sound, only
location-keyed dispositions are re-judged.

## Resolution

On landing: give survivors a position identity stable across renames
(anchor-relative line + operator, or file-content-addressed), rebase
against it, promote this rationale into results.md beside the rebase
clause, then delete this doc — git holds history.
