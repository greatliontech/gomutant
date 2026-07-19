# Ephemeral compile discards hide the compiler error

Lands: when ephemeral/manual-mutant compile failures include the underlying Go
compiler error and enough context to repair the manual edit.

## Observed

A manual MCP `gomutant_ephemeral` probe replaced a terminal predicate return
expression with `return false`. The mutant did not compile because the edited
function no longer referenced an imported package, leaving an unused import. The
tool response was only:

`mutant did not compile: nothing was measured -- check the replacements for
cmd/stash/main.go`

The fix was straightforward once inferred: keep the import live in the manual
edit with `_ = term.IsTerminal`, or make a batch edit that also removes the
import. The tool did not show the actual compile diagnostic, so the user had to
guess why the replacement did not build.

## Resolution

For ephemeral runs, include the attributed compiler stderr or structured build
error when a manual replacement fails to compile. The message should name the
package, file, and diagnostic line when available. If the failure is an unused
import caused by deleting the only reference, the generic compiler text is
already enough; gomutant does not need custom repair advice as long as it exposes
the compiler reason.

This is separate from ordinary generated-mutant discards: manual probes are
interactive evidence gathering, so compile failures should be directly
actionable.
