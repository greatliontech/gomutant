# Mutation

A mutation is a small, syntactic change to a target's body that a competent
test should notice. gomutant generates them from a fixed operator set and
runs each in isolation.

**body** (term): the source of a symbol's implementation — the function or
method body when there is one, the whole declaration otherwise. The version
of behavior a finding is measured against.

**mutant** (term): one body with exactly one operator applied. The unit that
is run and either killed or reported as a survivor.

**REQ-mut-operators** (behavior): The operator set MUST comprise condition
negation; comparison, logical, and arithmetic operator swaps, including their
compound-assignment and increment/decrement forms; boolean-operand forcing;
integer-literal increments; break/continue swaps; statement deletion —
dropping an assignment's store while still evaluating its right-hand side, so
removal-class mutants compile — and zero-value return substitution, each
applied syntactically where the replacement compiles without new imports or
type context — a site with no such counterpart (a return of a named or
struct type with no zero literal, the modulus operator with no swap partner)
yields no mutant there, a narrowing that can only miss kills, never
fabricate one. A mutant that fails to compile, does not differ from
the baseline, or renders identically to an earlier mutant is discarded (a
timed-out run is a kill — REQ-exec-attribution). The set carries a version
identifier so a finding records which operators generated it.

**REQ-mut-budget** (behavior): A run MUST accept a per-symbol mutant budget,
bounding how many mutants a target generates so an incremental run completes
quickly, with the exhaustive run — every operator at every applicable site —
available when the budget is unset. A finding records the budget it ran
under: a capped finding never answers a request for more mutants than it
generated.

**REQ-mut-overlay** (behavior): gomutant MUST apply a mutant through a build
overlay that leaves the working tree untouched, compiling and running it in
isolation — concurrent mutants share nothing but a content-addressed build
cache, so a run neither corrupts the working copy nor lets one mutant's
compilation perturb another's. The purity extends to run time: a mutant run
must not write into the tree through the tests it executes either — a
property-test library that persists a failure reproducer on a mutant-induced
failure is run with that persistence disabled, both to keep the tree clean
and so one mutant's reproducer can never replay into the next mutant's run.
