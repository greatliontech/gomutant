# Mutation

A mutation is a small, syntactic change to a target's body that a competent
test should notice. gomutant generates them from a fixed operator set and
runs each in isolation.

**body** (term): the source of a symbol's implementation — the function or
method body when there is one, the whole declaration otherwise. The version
of behavior a finding is measured against.

**mutant** (term): one body with exactly one operator applied. The unit that
is run and either killed or reported as a survivor.

**REQ-mut-operators** (behavior): gomutant MUST identify the exact active
operator basis in every finding. The current active `go/8` basis comprises
the equality, relational-boundary, relational-negation, and logical families
exactly as cataloged below; the boolean-operand, condition, range-suppression,
and loop-control families exactly as cataloged below; the arithmetic, bitwise,
and shift families exactly as cataloged below; compound-assignment and increment/decrement swaps;
integer-literal increments; statement deletion, with assignment stores dropped while right-hand
sides still evaluate; and zero-value return substitution, each emitted where
the replacement can be formed without a new import or named type expression. A
site with no such counterpart, including a result type with no context-free
zero expression, yields no `go/8` candidate. A selected candidate
that fails to compile, does not differ from the baseline, or renders identically
to an earlier selected candidate is discarded; a timed-out oracle run is a kill under
REQ-exec-attribution. When INV-MUT-COMPREHENSIVE lands, its catalog supersedes
the active list under a new identifier. Before then, a transitional basis may
activate only when the same change updates this clause with its exact identifier
and membership, lands complete catalog families rather than partial mappings,
and satisfies the candidate, accounting, stale-pin, and grammar contracts for
every active family.

The completed `go/8` families use their catalog labels. Its remaining labels
are exactly the token mappings `<old> -> <new>` for compound-assignment and
increment/decrement swaps, plus `increment literal`,
`delete statement`, `drop assignment`, and `zero return`. It differs from
`go/7` only by completing the bitwise and shift families named
above; all other operator sites, replacements, candidate ordering, and
accounting are unchanged.

**INV-MUT-COMPREHENSIVE** (project invariant): The comprehensive automatic
basis MUST be the finite first-order catalog below. Every mapping applies once
at every applicable original source site, with static type information used to
admit the site but without adding an import, identifier, helper declaration,
temporary, or named type expression. One candidate changes one catalog site;
deterministic formatting and pruning imports made unused by that change are
normalization, not additional mutation sites. Any change to catalog membership,
mapping, applicability, ordering, or deduplication receives a new operator-set
identifier before its findings can be reused.

Lands: when the automatic generator and finding accounting implement this
catalog under an operator-set identifier distinct from `go/3`.

The token-replacement families and their ordered variants are:

| Family | Applicable site | Ordered replacements |
|---|---|---|
| equality | equality binary expression | `==` → `!=`; `!=` → `==` |
| relational boundary | ordered binary expression | `<` → `<=`; `<=` → `<`; `>` → `>=`; `>=` → `>` |
| relational negation | ordered binary expression | `<` → `>=`; `<=` → `>`; `>` → `<=`; `>=` → `<` |
| logical | boolean binary expression | `&&` → `||`; `||` → `&&` |
| arithmetic | numeric binary expression | `+` → `-`; `-` → `+`; `*` → `/`; `/` → `*`; integer `%` → `*` |
| bitwise | integer binary expression | `&` → `|`; `|` → `&`; `^` → `&`; `&^` → `&` |
| shift | shift binary expression | `<<` → `>>`; `>>` → `<<` |
| unary | unary expression | numeric `+` → `-`; numeric `-` → `+`; boolean `!` → its operand; integer `^` → its operand |
| compound arithmetic | compound assignment | `+=` → `-=`; `-=` → `+=`; `*=` → `/=`; `/=` → `*=`; integer `%=` → `*=` |
| compound bitwise | compound assignment | `&=` → `|=`; `|=` → `&=`; `^=` → `&=`; `&^=` → `&=` |
| compound shift | compound assignment | `<<=` → `>>=`; `>>=` → `<<=` |
| compound store | compound assignment | `+=` → `=`; `-=` → `=`; `*=` → `=`; `/=` → `=`; `%=` → `=`; `&=` → `=`; `|=` → `=`; `^=` → `=`; `&^=` → `=`; `<<=` → `=`; `>>=` → `=` |
| increment/decrement | increment or decrement statement | `++` → `--`; `--` → `++` |
| loop control | branch statement | `break` → `continue`; `continue` → `break` |

Token-family operator labels are exactly `<family>: <old> -> <new>`, using the
family spelling in the first column and ASCII arrows. Unary removal uses
`unary: ! -> identity` and `unary: ^ -> identity`. The rows above have global
family ranks 1 through 14 in table order; each replacement has its row-local
variant rank in listed order.

Ordered operands include strings; arithmetic excludes string concatenation.
Integer domains include defined types whose underlying type is integer. Numeric,
boolean, string, ordered, and nil-capable domains likewise include defined types
with that underlying form. A type parameter is applicable only when every type
in its type set admits the replacement. Compound replacement must be assignable
to its original left operand. A branch swap is applicable only when the new
branch has a valid lexical or labeled target. Constant representability,
division by constant zero, and other context-sensitive rejection are decided by
compilation and produce a discard.

The ordered scalar-value families are:

| Family and operator label | Applicable site | Replacement |
|---|---|---|
| `integer literal: magnitude +1` | integer literal | arbitrary-precision magnitude plus one, rendered as canonical decimal |
| `rune literal: value +1` | rune literal | decoded rune value plus one, rendered by Go's `strconv.QuoteRune` canonical spelling |
| `float literal: value +1` | floating-point literal | `(<original literal> + 1.0)` |
| `imaginary literal: value +1` | imaginary literal | `(<original literal> + 1i)` |
| `boolean literal: true -> false` | universe `true` identifier | `false` |
| `boolean literal: false -> true` | universe `false` identifier | `true` |
| `string literal: nonempty -> empty` | non-empty interpreted or raw string | `""` |
| `string literal: empty -> nonempty` | empty interpreted or raw string | `"mutant"` |

Literal mutation follows the lexical literal, not a surrounding unary sign, so
mutating `-1` yields `-2`. Shadowed identifiers named `true` or `false` are not
boolean literals. Invalid rune values, overflow, duplicate cases, invalid array
lengths, and other context failures are discarded by compilation.
The scalar-value rows have global family ranks 15 through 22 in table order.

The ordered boolean and control-flow families are:

| Operator label | Applicable site | Replacement |
|---|---|---|
| `boolean operand: -> true` | each direct operand of `&&` | `true`, left before right |
| `boolean operand: -> false` | each direct operand of `||` | `false`, left before right |
| `condition: negate` | `if` and condition-bearing classical `for` condition | `!(condition)` |
| `condition: force true` | `if` condition | `true` |
| `condition: force false` | `if`, every classical `for` condition, and conditionless `for` | `false`, inserting the missing loop condition |
| `range body: prepend break` | range body | an unlabeled `break` before its first original statement |
| `block: empty` | `if` body, `else` block, expression/type-switch case body, or select communication body | remove every direct body statement |

Logical-operand forcing uses only the identity values listed above. A loop
condition is never forced true.
Expression-switch tags, case expressions, type-switch guards, and select
communications are not condition sites, but operators and literals inside them
remain ordinary sites. Range suppression still evaluates the range expression
and performs first-iteration assignment before breaking. Loop bodies and whole
function bodies are not emptied.
The boolean/control rows have global family ranks 23 through 29 in table order.

`statement: delete` applies to each direct statement in a block, switch case, or
select communication body except declarations, short declarations, and
assignments. It therefore includes expression calls, `panic`, increment,
send, `go`, `defer`, return, branch, label, nested block, condition, loop,
switch, type-switch, and select statements when deletion compiles.
`assignment: drop store` applies to every non-short assignment in a statement
list, initializer, or post statement: each left operand becomes `_`, preserving
the syntactic right-hand-side evaluations. Compound assignment becomes `_ =`
its right operand. Evaluation used only to address the original left operand is
not preserved. Declarations and short declarations have no deletion mutant.
`statement: delete` and `assignment: drop store` have global family ranks 30
and 31.

Return substitution uses the declared function result slot, not the original
expression's narrower type, and generates one candidate per explicit result
expression and applicable ordered replacement below; sibling result expressions
are unchanged:

| Operator label | Static result domain | Replacement |
|---|---|---|
| `return: false` | boolean | `false` |
| `return: true` | boolean | `true` |
| `return: zero` | integer, floating-point, complex, or string | `0` for numeric; `""` for string |
| `return: nil` | pointer, slice, map, channel, function, interface, or unsafe pointer | `nil` |

Defined types with a listed underlying form are included where the untyped
replacement is assignable. Bare returns, one expression supplying multiple
results, arrays, structs, and type parameters have no return substitution.
The four return rows have global family ranks 32 through 35 in table order.

The comprehensive basis explicitly excludes declaration and `:=` mutation;
`goto` and `fallthrough` replacement; unary `&`, `*`, and `<-`; selector,
field, package qualifier, type argument, type assertion, conversion, composite
element, map-key, struct-tag, array-type, and channel-direction replacement;
call, receiver, method-name, or argument deletion/reordering/substitution;
automatic error, panic, nil-dereference, delay, scheduling, race, or
concurrency injection; and every rewrite requiring a second source site. Calls
used as statements remain deletable, their subexpressions remain mutable, and
domain-specific or multi-site changes remain available through ephemeral
mutation.
These exclusions suppress only a whole-node mutation family. Every otherwise
applicable descendant remains a site, including literals and operators in call
arguments, conversions, composite elements, and map keys.

A **candidate** is one catalog variant at one applicable original AST site
before rendering or deduplication. A budget selects candidates, including ones
later discarded. Failure to render or format the one-site rewrite, normalize
its imports, or construct its overlay is a generator/infrastructure error and
aborts the measurement. A selected candidate is **discarded** when its
canonical body equals the baseline, its normalized complete overlay equals an
earlier selected candidate, or the compiler rejects the mutated overlay before
an oracle test runs. Compiler rejection is attributable to the candidate only
after the same package-scoped baseline passed and source/build inputs remained
coherent; baseline, environment, unrelated-package, malformed-output, and
movement failures abort under REQ-exec-attribution. Every selected candidate is
exactly one discard, kill, or survivor:

```text
generated = discarded + killed + survived
mutants = killed + survived
generated = mutants + discarded
```

The finding totals and each operator row obey those equations. Budget zero
selects every candidate. Positive budget `N` selects the first
`min(N, candidate count)` candidates. Enumeration always records the total
applicable `candidateCount`, so a request and finding can distinguish an exact
exhaustive boundary from a capped prefix. Decisions report the selected
candidate count, including candidates later discarded.

Candidate order is ascending original file path, start byte, end byte, global
family rank, variant rank, then operand/result index. Ordering spans are the
original operator token for token replacements; the complete unary expression
for unary replacement; the literal or identifier for scalar replacement; the
operand for operand forcing; the original condition for condition replacement;
the `for` token for an inserted condition; the complete range statement for
range suppression; the body braces for block emptying when braces exist; a
case/communication clause's colon through its last direct statement for a
clause body, or its colon alone when empty; the complete statement or assignment
for deletion/store dropping; and the original result expression for return
substitution. A token used as a zero-width insertion span ends at
the end of that token. A smaller original source span precedes a larger span at
one start byte.
Deduplication retains the first candidate and discards each later duplicate.
Positions name the original operator token for token replacements, original
operand/result start for value replacement, original condition start for
condition replacement, the `for` token for an inserted condition, opening
brace for braced-body emptying, clause colon for clause-body emptying, statement
start for deletion, assignment start for store dropping, and the `range` token
for range suppression. Existing source-order occurrence
suffixes disambiguate a repeated position and operator. Worker count cannot
change candidate selection, identities, summaries, or findings.

**REQ-mut-budget** (behavior): A run MUST accept a per-symbol candidate budget,
bounding how many candidates a target selects so an incremental run completes
quickly, with the exhaustive run — every operator at every applicable site —
available when the budget is unset. A finding records the budget it ran
under: a capped finding never answers a request for more candidates than it
generated. Under INV-RESULT-CANDIDATE-CONSERVATION, zero is the exhaustive
request and positive `N` requests the first
`min(N, candidateCount)` candidates. The budget component of coverage holds
when either the prior finding's `generated` equals `candidateCount`, or its
`generated` is at least that request's selected candidate count; every other
REQ-result-stale pin remains required.

**REQ-mut-overlay** (behavior): gomutant MUST apply a mutant through a build
overlay that leaves the working tree untouched, compiling and running it in
isolation — concurrent mutants share nothing but a content-addressed build
cache, so a run neither corrupts the working copy nor lets one mutant's
compilation perturb another's. The purity extends to run time: a mutant run
must not write into the tree through the tests it executes either — a
property-test library that persists a failure reproducer on a mutant-induced
failure is run with that persistence disabled, both to keep the tree clean
and so one mutant's reproducer can never replay into the next mutant's run.
