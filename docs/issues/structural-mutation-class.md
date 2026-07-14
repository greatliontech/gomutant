# No structural mutation class — analyzer-shaped oracles get no teeth check

Lands: when a caller needs adequacy evidence for an oracle that asserts a
structural property (an import boundary, an interface satisfaction) rather
than a function body's behavior.

The operator set mutates function bodies reached through the target set. A
structural assertion has no implementing body — its subject is the module's
import graph or type relations — so its oracle never gets a teeth check:
there is nothing to mutate, and a vacuous oracle passes forever.

The mutation class such oracles need is structural, not expression-level:
synthesize the forbidden state in a scratch copy of the tree and require
the oracle to fail —

- for an import-boundary assertion: inject a blank import of the forbidden
  package into a package the assertion scopes; the oracle must fail naming
  a chain;
- for an interface-satisfaction assertion: remove or break the asserted
  method set; the oracle must fail.

A consuming project ran exactly this loop by hand — blank imports injected
into three scoped packages, the oracle required to name the full chain each
time, then restored. That is the mechanical break-observe-restore cycle
this tool exists to own, applied one level up.

A structural-mutation kill is evidence about the *oracle's* teeth, never
about the soundness of whatever analyzer backs it; the caller's trust
model for analyzers is out of scope here. Provenance: migrated from
stipulator (`git log --all -- docs/issues/analyzer-witness-hardening.md`
there) when mutation ownership moved to gomutant.
