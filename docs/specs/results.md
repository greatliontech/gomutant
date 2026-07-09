# Results

A finding is only as trustworthy as its provenance. gomutant records every
input that shaped a result and treats the record as valid only while those
inputs still hold, so a stale finding re-measures rather than misleads.

**body hash** (term): a hash of a body's canonical text, ignoring formatting
churn. It moves when behavior-bearing code moves, and it is how a record
detects that a target's body or an oracle test has changed.

**REQ-result-record** (behavior): A finding record MUST be keyed by the
mutated symbol and pin the inputs that produced it — the symbol's body hash,
the oracle as a set of test symbols each with its own body hash, the operator
version, the mutant budget, and the identity of the toolchain that ran the
mutants — carrying the mutant count, the kill count, each survivor's position
and operator, and the body's first-line anchor that positions are rebased
against (REQ-attest-survivor). The oracle is pinned by content, not merely by
name: strengthening a test moves its body hash, so a record cannot keep
reporting a survivor a now-sharper test would kill. The toolchain identity
carries the platform — version and GOOS/GOARCH — because the same body under
the same oracle kills differently across toolchains and platforms, so records
are per-platform by construction: a team spanning platforms regenerates from
one designated platform rather than ping-ponging the store.

**REQ-result-tolerant** (behavior): Loading a finding record MUST tolerate an
unrecognized field by discarding it rather than refusing the document. The
tolerance is safe because its direction is anti-flattering: every open
finding is a genuinely measured survivor, so a dropped field can re-stale
the record (a missing pin no longer covers the request — REQ-result-stale)
or widen the open set (a dropped disposition-bearing field puts attested
survivors back among open findings), but can never serve a kill or an
equivalence the inputs don't back. A wrongly widened open set costs a
re-judgment or a spurious caller-policy failure — the safe direction —
where a wrongly served claim would be the corrupted flattering measurement
the keystone refuses (REQ-core-attributed-kills). Tolerance governs unknown
*fields* within an understood document; an unknown document *version* is the
structural boundary and is rejected per REQ-result-export's version tag.

**REQ-result-stale** (behavior): gomutant MUST re-measure a target rather
than serve a record whose pins no longer cover the request — an edit to the
body, a strengthened or added oracle test, a new operator version, a
toolchain change, or a request for more mutants than a capped record
generated each moves a pin and invalidates the record exactly as a body edit
does. A record is never partially trusted: any moved pin re-measures the
whole target.

**REQ-result-export** (structural): Findings MUST be serializable to a
portable, versioned document that gomutant owns — carrying, per mutated
symbol, the pins that scope the record (body hash; the oracle as test symbols
each with its body hash; operator version; budget; toolchain), the mutant and
kill counts, each survivor's position and operator, and each attested
disposition with its reason. A version tag lets a consumer reject a document
it does not understand. This is the inverse of the targeting seam: gomutant
parses a producer's format going in (REQ-target-producers) but owns the
result format going out, so a downstream reader — a dashboard, a CI step, or
stipulator recovering findings by label — consumes gomutant's contract, never
its internal store.

**REQ-attest-survivor** (behavior): A survivor MUST be dispositionable as
equivalent with a recorded reason, refused unless the named mutant is among
the record's current survivors, and shed whenever any pin moves — every body,
oracle, or operator version's equivalences are judged afresh, and a record's
open findings are its survivors less its attested ones. Positions are
location metadata, rebased against the record's body-line anchor
(REQ-result-record) when carried across regenerations: drift from edits
outside the body never sheds a disposition.

**REQ-result-findings** (behavior): gomutant MUST present survivors as
findings awaiting disposition, never as a pass/fail verdict — strengthen a
test or attest an equivalence — so whether an open survivor should fail a
build is a policy the caller applies to the findings, not a judgment the tool
bakes in.
