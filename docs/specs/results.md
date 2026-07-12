# Results

A finding is only as trustworthy as its provenance. gomutant records every
input that shaped a result and treats the record as valid only while those
inputs still hold, so a stale finding re-measures rather than misleads.

**body hash** (term): a hash of a body's canonical text, ignoring formatting
churn. It identifies mutant positions and changed-scope candidates; it is not
freshness evidence.

**subject evidence** (term): gomutant-owned persisted data for one target or
oracle subject: its identity, maximal Gofresh source-closure hash, code-result
toolchain and build-configuration guards, attributable purity assertion, and the
finding's merged runtime-input manifest, digest, and explicit unverifiable
disposition, including incomplete-process reasons.

**REQ-result-record** (behavior): A finding record MUST be keyed by the
mutated symbol and pin the inputs that produced it — target subject evidence,
the oracle as a set of distinct subject evidence records, the operator version,
the mutant budget, and the exact effective per-mutant timeout encoded as a
canonical Go duration string — carrying the capture commit and dirty provenance,
the mutant count, the kill count, each survivor's position
and operator. The oracle is pinned by identity and complete Gofresh evidence,
not merely by name: strengthening a test or any source it
depends on moves its closure, so a record cannot keep reporting a survivor a
now-sharper test would kill. The merged runtime-input evidence is attached to
every subject because process-wide observations cannot soundly be attributed
more narrowly. Dirty provenance bars a finding from explicit committed-baseline use
but does not prevent reuse in the unchanged working tree. The commit is omitted only
when no repository HEAD exists; that unavailable provenance carries `dirty=true`.

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
target or any target/oracle dependency, a changed runtime input, purity,
toolchain, or build configuration, an added or removed oracle identity, a new
operator version, a different effective timeout, or a request for more mutants
than a capped record generated each invalidates the record. Every target and
oracle Gofresh verdict must be valid; stale or unverifiable remeasures. A record
is never partially trusted: any moved pin remeasures the whole target.

**REQ-result-export** (structural): Findings MUST be serializable to a
portable version-1 document that gomutant owns — carrying, per mutated
symbol, the pins that scope the record (target and oracle subject evidence;
operator version; budget; timeout; commit and dirty provenance), the mutant and
kill counts, each survivor's position and operator, and each attested
disposition with its reason. A version tag lets a consumer reject a document
it does not understand. This is the inverse of the targeting seam: gomutant
parses a producer's format going in (REQ-target-producers) but owns the
result format going out, so a downstream reader — a dashboard, a CI step, or
stipulator recovering findings by label — consumes gomutant's contract, never
its internal store. A clean break changes the version-1 shape directly;
documents missing any required version-1 field are malformed and refused.

**REQ-attest-survivor** (behavior): A survivor MUST be dispositionable as
equivalent with a recorded reason, refused unless the named mutant is among
the record's current survivors, and shed whenever any pin moves — every subject
evidence or mutation-domain pin's equivalences are judged afresh, and a record's
open findings are its survivors less its attested ones. Positions are location
metadata only: a remeasurement under identical pins carries a disposition only
when the same position and operator survive again; source-evidence drift sheds it
rather than attempting to infer that a closure change was location-only.

**REQ-result-findings** (behavior): gomutant MUST present survivors as
findings awaiting disposition, never as a pass/fail verdict — strengthen a
test or attest an equivalence — so whether an open survivor should fail a
build is a policy the caller applies to the findings, not a judgment the tool
bakes in.

**REQ-result-hygiene** (behavior): A whole-tree run MUST remove findings for
symbols absent from its complete discovery snapshot, including when the tree
contains no targets, because such records can never be measured again and
presenting their survivors as open would mislead callers. Changed-scope and
explicit-target runs retain every unmeasured document entry: their target sets
assert only what to measure, never that an omitted symbol no longer exists.
