# Results

A finding reports a completed mutation measurement. gomutant also records the
available provenance for deciding whether that measurement can be reused: a
record is served without execution only while every required input still holds,
while stale or unverifiable evidence re-measures rather than misleads.

**body hash** (term): a hash of a body's canonical text, ignoring formatting
churn. It identifies mutant positions and changed-scope candidates; it is not
freshness evidence.

**subject evidence** (term): gomutant-owned persisted data for one target or
oracle subject: its identity, maximal Gofresh source-closure hash, its
package's test-variant compartment hash, code-result
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
canonical Go duration string — carrying the target package's test-variant
compartment ledger (the declaration-level record the growth carve-out diffs
at serve time), the capture commit and dirty provenance,
the mutant count, the kill count, each kill's candidate identity and killer
(the killing oracle test's symbol, the timeout marker, or the package-failure
marker — REQ-core-attributed-kills made durable; a record carries either
every kill's attribution or none, and a partial list is malformed and
refused), each survivor's position
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
min(budget, candidateCount)` when budget is positive. A record merged from a
served prefix and a measured remainder — the candidate re-execution splice or
the budget extension per REQ-result-stale — satisfies the same equations over
its merged totals: conservation is single-run and merged-provenance alike. A
document violating a
count equation or budget relation is malformed and refused.

INV-RESULT-CANDIDATE-CONSERVATION: enforced by
`TestRunConservesCandidateDiscards`,
`TestSpliceFindingCountsConservesChangedOutcomes`,
`TestExtendFindingCountsAppendsSuffixOutcomes`,
`TestGrowFindingCountsReplacesSurvivorOutcomes`, and
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
oracle selection mode or operator version, or a different effective oracle timeout each invalidates the record; a
request for more candidates than a capped record generated invalidates only
the unmeasured remainder, under the budget-extension carve-out below. Every target and
oracle Gofresh verdict must be valid; stale or unverifiable remeasures.
Measurement pins are never partially trusted: any moved pin remeasures the
whole target, with exactly four precisely scoped carve-outs — candidate-local
evidence, the budget when it is the only pin that fails to cover, derived
oracle growth under an inert test-variant delta, and killer-scoped oracle
drift under an attributable test-variant delta.
Candidate evidence is the first narrower axis: a record whose only
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
explicitly non-reusable. The budget is the second carve-out: candidate
enumeration is deterministic — a stable global order over the unchanged body,
pinned by the record's target evidence and operator set — so when every pin
except the budget holds (target and oracle evidence valid, equal oracle set
and selection mode, equal operator set and effective oracle timeout, no
candidate evidence on the record), the recorded prefix remains exact evidence
for candidates `[0, generated)` and the run measures only the unmeasured
suffix `[generated, needed)`. The splice appends the suffix outcomes onto the
record: prefix dispositions, survivor identities, their attestations, and
their advisory execution buckets carry
verbatim — their pins did not move — suffix survivors and per-operator counts
are appended, the suffix run's candidate evidence becomes the record's, and
`budget`, `generated`, and `candidateCount` record the merged truth. The
extension is bounded fail-closed by the re-execution splice's own rules: it
never composes with candidate evidence — a capped record carrying flagged
candidates re-measures whole under a wider request, the decision naming why;
deterministic regeneration must re-identify the recorded prefix (an unchanged
complete candidate count, unique candidate identities, the recorded
per-operator selected counts over the prefix, and every recorded survivor
inside it) or the target remeasures whole; and the suffix processes' completed
union, merged over the record's persisted union, must still equal that
persisted union — a suffix process that read runtime inputs the record never
pinned preserves the extended outcome but stamps it explicitly non-reusable. A
forced run re-measures whole regardless. The extension's decision reports the
served prefix and the measured suffix ("served: prefix of N candidates stands;
measuring M more", the candidate noun count-aware: a one-candidate prefix
reads "1 candidate"). Derived oracle growth is the third carve-out, resting
on the keystone: every recorded kill names its killer among the recorded
oracles (REQ-core-attributed-kills), and timeout and package-scope kills rest
on the same recorded set's behavior, so a grown oracle cannot un-kill
anything — it can only kill more. When the finding and the request are both
non-explicit — growth is a derived-oracle claim on both sides; an explicit
request that happens to superset the recorded derived set is the caller's
selection, never derived growth — the
current derived set is a strict superset of the recorded one, and every other
pin covers — scalar pins equal; no candidate evidence; the target's and every
retained oracle's evidence checking plainly valid, target-package subjects
with their recorded compartment pin refreshed to the current one: the
refresh is licensed exactly by an inert declaration delta — the finding's
recorded compartment ledger diffed against the current one classifies
inert, so the compartment moved by additions no unchanged declaration can
observe, and anything changed, removed, or initialization-bearing
re-measures whole — and the check stays plain because gofresh orders the
compartment comparison before the environment tiers, so accepting the stale
"test variants" verdict instead would let a moved pin hide behind it — then
recorded outcomes stand for
every candidate except the survivors, which the run re-measures against only
the added test names: a killed candidate stays killed, a discard stays
discarded, a survivor an added test kills moves to killed with the kill
attributed like any other, and a still-surviving candidate keeps its survival
with its execution bucket re-derived honestly — a never-executed survivor an
added test executes becomes executed-and-passed. The delta run captures
evidence for the added oracles; the grown record carries the current tree's
evidence for every subject — the gate itself proved the retained subjects'
only movement is the inert compartment delta — and the current compartment
ledger. Attestations carry for still-surviving attested candidates; a newly
killed attested candidate loses its attestation — evidence beats
attestation — with each contradiction reported through the run's
attestation-contradiction report, naming the survivor, its shed reasoning,
and the killer. The carve-out is
bounded fail-closed like its siblings: it never composes with candidate
evidence or a budget shortfall; deterministic regeneration must re-identify
the record's candidates and survivors or the target re-measures whole; the
delta processes' completed union, merged over the record's persisted union,
must equal that persisted union or the grown finding is preserved but
explicitly non-reusable; a failing added test on the clean tree refuses at
the per-group baseline exactly as any baseline failure; a record persisted
without a compartment ledger re-measures whole; and a forced run re-measures
whole. The growth decision reports "served: derived oracle grew by N tests;
re-measuring M survivors against them" (count-aware nouns) with `candidates`
counting the re-measured survivors. Killer-scoped oracle drift is the fourth
carve-out, resting on the keystone made durable: every recorded kill names
its killer beside the kill, so when the target package's compartment moves
in a way the declaration ledger can attribute — or holds still while some
oracle's own evidence moves; the partition is by what each kill rests on,
and an empty delta partitions by the evidence signal alone — an edit a
killer provably
cannot observe cannot un-kill its kill. The delta is attributable exactly
when every added, changed, or removed declaration is a plain function (never
TestMain), a method of a receiver type declared as a compartment type in
the same compartment package (receiver types resolve within their own
package, so a name-only match against the other variant's type certifies
nothing, and an entry recorded without its package clause certifies
nothing), a const, or a
type, and no embedded member's header moved — the rejected kinds each reach
unchanged tests without any reference (a package var's initializer and an
init function run at test-binary initialization, TestMain wraps every test,
a directive is behavior-bearing from any position, a method of a receiver
type declared outside the compartment can flip interface satisfaction
observed by code the ledger cannot see, and an embedded member's bytes feed
unchanged code as data) — and additionally no unchanged unconditional
root — a package var's initializer, an init function, or TestMain — can
reach a delta declaration through the same reference walk: such a root runs
changed code around every test without any oracle naming it, so a reaching
root re-measures the whole target. Under an attributable delta each oracle classifies
moved or unmoved by two independent signals: its own evidence checking
plainly valid — target-package subjects with their compartment pin refreshed
to the current one, the refresh licensed by the attributable delta; any
other subject's evidence as recorded, its own package's compartment being
untouched by this delta — and, for target-package oracles, a reference walk
over the current ledger's referenced-name lists (the current ledger's lists
speak for every unchanged declaration: equal hashes pin equal bytes, and
the ledger's one stated fold exception always carries the governing entry's
own name, whose movement is what the walk must observe): from the oracle
test's own declaration through every name it
references, every same-named declaration, and every method of a receiver
type it reaches — reflection's only route to a compartment function — with
the walk observing a delta declaration, an unknown starting declaration, or
a served declaration with no reference list each classifying the oracle
moved, fail-closed. When the record carries complete kill attribution and a
compartment ledger, the oracle identity sets are equal (an added or removed
oracle identity is growth's or the general rule's domain, never drift's),
the scalar pins are equal, there is no candidate evidence, and the target's
evidence checks plainly valid under the same refresh: kills whose recorded
killer is an unmoved oracle stand, and every other candidate re-measures
against the full current oracle — kills whose killer moved (the other
oracles' outcomes on that candidate were never recorded), timeout and
package-scope kills whenever any oracle moved (they rest on the whole
recorded set's behavior), and, whenever any oracle moved, every survivor (a
moved test may now kill it; with nothing moved, survivals stand exactly like
kills) — with re-measured survivors' advisory buckets re-derived from the
current probe, a newly killed attested survivor shedding its attestation
through the same contradiction report as growth, and discards standing (a
compile rejection is a pure function of the mutant source under the
toolchain and build-configuration pins). The drifted record carries the
current tree's evidence for every subject and the current compartment
ledger. The carve-out is
bounded fail-closed like its siblings: it never composes with candidate
evidence or a budget shortfall; a record without complete kill attribution
or without a compartment ledger re-measures whole; deterministic
regeneration must re-identify the record's candidates, kills, and survivors
or the target re-measures whole; the re-measure processes' completed union,
merged over the record's persisted union, must equal that persisted union or
the drifted finding is preserved but explicitly non-reusable; and a forced
run re-measures whole. A delta reaching no recorded oracle serves the whole
record with nothing re-measured. The drift decision reports "served: N kills
stand on unmoved oracles; re-measuring M candidates against the current
oracle" (count-aware nouns; a delta reaching no oracle reports "served:
compartment delta reaches no recorded oracle; nothing re-measures") with
`candidates` counting the re-measured candidates. When
INV-RESULT-CANDIDATE-CONSERVATION applies, a zero-budget request requires
`generated == candidateCount`; a positive request `N` requires `generated >=
min(N, candidateCount)`. A stronger exhaustive or longer-prefix finding may
serve a weaker request without remeasurement. Every serve rewrite — the
cached serve, the candidate re-execution splice, the budget extension,
derived-oracle growth, and killer-scoped drift — records commit and dirty
provenance recomputed from the current tree, exactly as a fresh measure
stamps them: the proof that licensed the serve validated every subject's
evidence and runtime manifest against the current tree, so a record
measured under dirty provenance becomes portable (REQ-result-layers) the
first time it serves with those paths clean, its attestations riding the
promotion. Each subject's manifest resolves against that subject's own
module directory — the base its validation used; an unreadable manifest,
or evidence naming a subject the run carries no view for, stamps dirty,
fail-closed. An explicitly non-reusable rewrite re-stamps like any other
but never promotes — its unverifiable subject evidence fails the
portable line regardless of provenance. Serve and re-measure decisions
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
portable version-3 document that gomutant owns — carrying, per mutated
symbol, the pins that scope the record (target and oracle subject evidence,
each carrying its package's test-variant compartment hash beside the maximal
closure;
oracle selection mode; operator version; budget; oracle timeout; commit and dirty provenance), the mutant and
kill counts, the kill-attribution list when the record carries one (its
absence is tolerated in the anti-flattering direction: a record without it
re-measures whole under the killer-drift carve-out rather than serving, so
the field widens reuse and rides the current version without a bump — the
inverse of candidate evidence's narrowing precedent), each survivor's
position and operator, the candidate-evidence
list when any candidate carries one, and each attested
disposition with its reason, and the per-operator disposition summary. A version tag lets a consumer reject a document
it does not understand. This is the inverse of the targeting seam: gomutant
parses a producer's format going in (REQ-target-producers) but owns the
result format going out, so a downstream reader — a dashboard, a CI step, or
a spec-driven producer recovering findings by label — consumes gomutant's contract, never
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
is discarded, never surfaced — the overlay is a cache, not a record. An
overlay entry larger than 64 MiB is discarded the same way, judged by the
size of the content a read would consume (symlinks followed) before any of
its bytes are read: a cache's cost discipline is a content discipline —
healthy per-symbol evidence sits orders of magnitude under the ceiling, so
only pathological residue such as the bloat of an abandoned format
regression crosses it, and an eviction costs at most a re-measure, never a
lost record of note. A merged
read re-reads and re-parses only overlay entries whose stat identity — size
and modification time of the content a read would consume — differs from the
last parse the reading store holds, so a run's incremental commits re-parse
only the entries that moved between commits, never the overlay's total
bytes; the per-symbol entry layout exists exactly so an unchanged entry
needs no re-read. Stat identity is deliberately approximate in the
already-tolerated direction: a rewrite it cannot distinguish — same size,
same modification time — serves the prior parse, a stale winner exactly like
the install-order races above, costing at most a re-measure and never a
wrong verdict, because the served record still carries and revalidates its
own evidence. A read
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
review carrying local execution facts. Findings surfaces — the findings faces
and the run faces' result rows (REQ-exec-run-status in
[execution.md](execution.md)) — name a local record's disqualifying reason,
so whether the artifact is safe to stage is answered by the tool, not by
inspecting JSON: CLI faces render only the machine-local marker, absence
meaning repo, while MCP rows carry the layer explicitly.

REQ-result-layers: enforced by `TestCommittableDrawsThePortableLine`,
`TestStoreSplitsUpdatesAcrossLayers`,
`TestStoreUpdateDecidesMembershipUnderTheDocumentLock`,
`TestRunCommandStatesMachineLocalRouting`,
`TestOverlayEvictsEntriesOverTheEvidenceCeiling`,
`TestOverlayCeilingFollowsSymlinkedEntries`,
`TestOverlayServesUnchangedEntriesWithoutReparsing`,
`TestOverlayInstallWarmsTheParseCache`,
`TestOverlayReloadTracksRewrittenAndDeletedEntries`, and
`TestOverlayMergedViewIsIsolatedFromCallerMutation`.

A survivor carries optional execution evidence — `never-executed`,
`executed-and-passed`, or `unstable-oracle` per REQ-exec-survivor-evidence in
[execution.md](execution.md) — advisory and empty on records measured before
bucketing existed; it is location metadata's sibling, never a measurement pin.

A survivor position is `file.go:line:column`. When distinct generated mutants
share that position and operator, the second and later identities append
`#<source-order occurrence>`. The discriminator is part of the survivor,
kill-attribution, and
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
