# Results

A finding reports a completed mutation measurement. gomutant also records the
available provenance for deciding whether that measurement can be reused: a
record is served without execution only while every required input still holds,
while stale or unverifiable evidence re-measures rather than misleads.

**body hash** (term): a hash of a body's canonical text, ignoring formatting
churn. It identifies mutant positions and changed-scope candidates; it is not
freshness evidence.

**subject evidence** (term): gomutant-owned persisted data for one target or
oracle subject: its identity, maximal Gofresh source-closure hash, code-result
toolchain and build-configuration guards, attributable observation-completeness
assertion, complete per-subject observability proof data, attributable purity
assertion, and the completed processes' merged runtime-input manifest, digest,
and explicit unverifiable disposition.

**candidate evidence** (term): the per-candidate runtime-evidence disposition: a
candidate measured by a process that could not prove its log complete carries an
explicit unverifiable marker with its incomplete-process reason and its measured
disposition — killed, survived, or discarded, so a splice can conserve the
generated-candidate accounting — identified by the candidate's position and
operator; every other candidate is covered by the subject evidence's
completed-process union. A compile-rejected candidate carries no candidate
evidence: no test process started — as reported by the test harness's own
build-failure event, never inferred from output text a test could forge — so
the run had no runtime exposure to prove complete, and its discard is a pure
function of the mutant source under the toolchain and build-configuration
pins; an oracle group that did run contributes its completed observation to
the union as usual.
The observation proof is encoded by required `observationAssertion`,
`observationStrategy`, `observationSubjectPackage`,
`observationSubjectSymbol`, `observationObservable`, and
`observationEvidence` fields plus `observationReason` exactly when the proof
disposition is not observable.

**REQ-result-record** (behavior): A finding record MUST be keyed by the
mutated symbol and record the available inputs that produced it — target subject evidence,
the oracle as a set of distinct subject evidence records, the operator version,
whether the oracle was explicit or package-derived, the candidate budget, and the exact effective oracle timeout in the `oracleTimeout` field encoded as a
canonical Go duration string — carrying the capture commit and dirty provenance,
the mutant count, the kill count, each survivor's position
and operator, plus per-operator generated, discarded, killed, and survived
counts whose sums equal the finding totals. The oracle is pinned by identity and complete Gofresh evidence,
not merely by name: strengthening a test or any source it
depends on moves its closure, so a record cannot keep reporting a survivor a
now-sharper test would kill. The completed processes' merged runtime-input evidence is attached to every
subject because a completed observation's content cannot soundly be attributed
more narrowly; incompleteness itself can — the incomplete process measured
exactly one candidate — so it is recorded as that candidate's evidence and
never widens to the finding. Dirty provenance bars a finding from explicit committed-baseline use
but does not prevent reuse in the unchanged working tree. The commit is omitted only
when no repository HEAD exists; that unavailable provenance carries `dirty=true`.

**INV-RESULT-CANDIDATE-CONSERVATION** (project invariant): Every finding
produced by a candidate-accounted active basis carries required `candidateCount` and
`generated` fields. `candidateCount` is the total applicable catalog candidates
before a budget; `generated` is the selected exhaustive set or positive-budget
prefix. The existing `mutants` field is the measured count after discards.
Finding and per-operator totals satisfy `generated = discarded + killed +
survived`, `mutants = killed + survived`, and `generated = mutants +
discarded`. Run decisions expose selected candidates as `candidates`, not as
measured mutants. `candidateCount` makes exact-budget exhaustion representable
without an additional exhaustive flag; every REQ-result-stale pin still applies.
All counts and `budget` are nonnegative, `generated <= candidateCount`, and the
record has `generated == candidateCount` when budget is zero or `generated ==
min(budget, candidateCount)` when budget is positive. A document violating a
count equation or budget relation is malformed and refused.

INV-RESULT-CANDIDATE-CONSERVATION: enforced by
`TestRunConservesCandidateDiscards`,
`TestSpliceFindingCountsConservesChangedOutcomes`, and
`TestParseFindingsCandidateEvidence`.

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
oracle selection mode or operator version, a different effective oracle timeout, or a request for more candidates
than a capped record generated each invalidates the record. Every target and
oracle Gofresh verdict must be valid; stale or unverifiable remeasures.
Measurement pins are never partially trusted: any moved pin remeasures the
whole target. Candidate evidence is the one narrower axis: a record whose only
unverifiable runtime evidence is candidate-local serves its covered candidates
and re-executes exactly the unverifiable ones under a passing current baseline
probe, conserving the generated-candidate accounting; the run decision reports
the serve with the re-executed candidate count. A candidate whose evidence
cannot prove its runtime inputs unchanged always re-executes, because a kill
retained past a moved runtime input its process could have read is the
forbidden flattering direction — a compile rejection is outside that rationale
(no process started, nothing could have been read) and serves covered under
the toolchain and build-configuration pins without re-execution. A record
persisted before this carve-out still carries compile-rejection candidate
evidence; its one remaining splice re-executes the rejection, produces no
fresh evidence, and the persisted spliced record serves fully thereafter. The serve is bounded fail-closed: when
deterministic regeneration cannot re-identify a flagged candidate the target
remeasures whole, and when the re-executed processes' completed union does not
equal the record's persisted union the spliced finding is preserved but
explicitly non-reusable. When
INV-RESULT-CANDIDATE-CONSERVATION applies, a zero-budget request requires
`generated == candidateCount`; a positive request `N` requires `generated >=
min(N, candidateCount)`. A stronger exhaustive or longer-prefix finding may
serve a weaker request without remeasurement. Serve and re-measure decisions
state their reason: a served record names the pins that held ("served: body,
oracle closure, and runtime inputs unchanged"; a splice adds its re-executed
candidate count), and a non-matching record names its inspection class
(stale, unverifiable, detached) and the moved pin best-effort via the
same attribution findings inspection uses, so a caller who just strengthened
an oracle sees the tool noticing rather than forcing a re-measure
defensively. Labels are correlation metadata,
not measurement pins: when every measurement pin still matches, a reused
finding adopts the current target's labels without remeasurement or shedding
survivor attestations. Oracle membership remains a measurement pin, so changing
the executable oracle remeasures as usual.

**REQ-result-export** (structural): Findings MUST be serializable to a
portable version-2 document that gomutant owns — carrying, per mutated
symbol, the pins that scope the record (target and oracle subject evidence;
oracle selection mode; operator version; budget; oracle timeout; commit and dirty provenance), the mutant and
kill counts, each survivor's position and operator, the candidate-evidence
list when any candidate carries one, and each attested
disposition with its reason, and the per-operator disposition summary. A version tag lets a consumer reject a document
it does not understand. This is the inverse of the targeting seam: gomutant
parses a producer's format going in (REQ-target-producers) but owns the
result format going out, so a downstream reader — a dashboard, a CI step, or
stipulator recovering findings by label — consumes gomutant's contract, never
its internal store. A field that narrows reuse — candidate evidence is the precedent — always
rides a version bump, because field tolerance in an older consumer would
otherwise serve the record with the narrowing silently dropped. A clean break
otherwise changes the current version's shape directly; documents missing any
required field of their version are malformed and refused.

**REQ-result-layers** (behavior): Findings persistence MUST split into two
layers by committability. The repo document (the findings path, under version
control) carries only portable records: clean commit provenance (not dirty,
commit present), no runtime-unverifiable subject evidence, and no runtime-input
path outside the module directory — evidence a reviewer on another machine can
inherit soundly. Every other record — dirty-worktree measurements,
unverifiable observations, machine-local input identities — lives in a
machine-local overlay under the user cache directory keyed by the resolved
module root, one atomically written entry per symbol; a malformed overlay entry
is discarded, never surfaced — the overlay is a cache, not a record. A read
merges both layers with the overlay winning per symbol. Overlay-wins is
install-order recency, not measurement recency: a crash between the repo write
and the overlay delete — or a slower writer's post-lock overlay install racing
a concurrent session's prune — can leave a stale local entry, shadowing a newer
committable repo row or resurrecting a pruned symbol, until that symbol's next
update — and a wrong winner costs a re-measure, never a wrong verdict, because
every record carries and revalidates its own evidence. A write decides membership under the repo
document's lock against the freshest merged state — a concurrent session's
committed rows are never evicted by a stale snapshot — and splits the updated
set by committability: a committable record replaces its repo row and deletes
its overlay entry; a local record installs into the overlay and never evicts a
repo row that still carries portable truth for its own pins; a symbol pruned
from the set leaves both layers. The split is
automatic — a developer never chooses between committing no evidence and
committing machine-local state, and full-sweep results stay shareable without
review carrying local execution facts. Findings surfaces state each record's
layer with the disqualifying reason for local records, so whether the artifact
is safe to stage is answered by the tool, not by inspecting JSON.

REQ-result-layers: enforced by `TestCommittableDrawsThePortableLine`,
`TestStoreSplitsUpdatesAcrossLayers`, and
`TestStoreUpdateDecidesMembershipUnderTheDocumentLock`.

A survivor carries optional execution evidence — `never-executed`,
`executed-and-passed`, or `unstable-oracle` per REQ-exec-survivor-evidence in
[execution.md](execution.md) — advisory and empty on records measured before
bucketing existed; it is location metadata's sibling, never a measurement pin.

A survivor position is `file.go:line:column`. When distinct generated mutants
share that position and operator, the second and later identities append
`#<source-order occurrence>`. The discriminator is part of the survivor and
attestation identity so overlapping syntax-tree mutation sites cannot collapse
into one disposition.
Under INV-RESULT-CANDIDATE-CONSERVATION, occurrence suffixes are assigned over
the complete globally ordered candidate set before budget selection or discard;
an earlier discarded candidate can therefore reserve an occurrence number.

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
Package- or symbol-filtered runs are likewise scoped and retain every
unmeasured entry, even when their targets came from whole-tree discovery.

**REQ-result-inspection** (behavior): Findings inspection MUST classify every
record as `current` when all recorded mutation-domain and subject evidence
still proves reusable, `stale` when a comparable input moved, `unverifiable`
when current evidence cannot prove reuse, or `detached` when the mutated symbol
no longer resolves. A record whose candidate evidence flags any candidate is
not reusable as it stands, so it classifies `unverifiable` even when its
subject evidence is current, with the candidate evidence carried in every view
so the candidate-local scope is visible. The classification is advisory and
runs no tests. Human and machine-readable views carry the reason plus open
survivors and attested dispositions independently of that state, including
fully attested records; filtering by an opaque label changes only which
records are rendered. The reason leads — it precedes the open survivors in
every view — and is self-contained: a subject-caused reason names the
responsible subject (`target:` or `oracle <symbol>:`), record-level causes
(a detached symbol, a changed operator set or derived oracle set,
candidate-local evidence) need no subject, and a runtime-input digest drift
names the moved input identities themselves, best-effort, so the developer decides
between stabilizing the test, narrowing the oracle, and accepting a
machine-local record without re-deriving which observed object moved.
