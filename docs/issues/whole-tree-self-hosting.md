# Whole-tree self-hosting

The repeatable explicit self-host matrix completes exhaustively, but expanding
self-hosting to every auto-discovered symbol with package-derived oracles has
not yet demonstrated one complete findings document.

Package-wide root tests create fresh temporary-file identities, and concurrent
mutant workers can observe movement in shared build-cache inputs. These
observations now preserve fresh mutation outcomes as explicitly non-reusable
findings rather than refusing the run, but serial and concurrent whole-tree
completion remain to be demonstrated. Weakening runtime-input identity or
marking fixture-driven tests pure would hide real dependencies, so neither is
an acceptable route to reusable evidence.

Lands: when an uncapped whole-tree run over every auto-discovered target
completes with package-derived oracle findings, every survivor is
strengthened or attested with a recorded reason, selected targets produce
complete findings with concurrent workers as well as one worker, unverifiable
evidence remeasures rather than caching, and target preparation reports
progress without requiring a multi-hour silent phase.

Reproduce from the repository root with:

```
gomutant run --jobs 1 --timeout 10m
```
