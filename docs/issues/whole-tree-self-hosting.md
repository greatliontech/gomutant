# Whole-tree self-hosting

The repeatable explicit self-host matrix completes exhaustively, but expanding
self-hosting to every auto-discovered symbol with package-derived oracles does
not yet produce one admissible findings document.

Package-wide root tests create fresh temporary-file identities, so repeated
runtime-input measurement can refuse a mutant as moved even when the mutated
symbol is unrelated. Concurrent mutant workers can likewise observe movement
in shared build-cache inputs; the explicit matrix is stable with one worker
and refused with four. Weakening runtime-input identity or marking fixture-
driven tests pure would hide real dependencies, so neither is an acceptable
workaround.

Lands: when an uncapped whole-tree run over every auto-discovered target
completes with stable package-derived oracle evidence, every survivor is
strengthened or attested with a recorded reason, selected targets produce
stable evidence with concurrent workers as well as one worker, and target
preparation reports progress without requiring a multi-hour silent phase.

Reproduce from the repository root with:

```
gomutant run --jobs 1 --timeout 10m
```
