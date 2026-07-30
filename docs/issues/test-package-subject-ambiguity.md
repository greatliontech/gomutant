# Same-named helpers across a directory's two test packages make the package unmeasurable

Lands: subject resolution keys on package identity (import path plus test-binary
role), so an identifier duplicated between `package x` and `package x_test` in one
directory is two distinct subjects rather than an ambiguity.

## Observed

Go permits the same identifier in a directory's internal test package (`package x`,
`_test.go`) and its external test package (`package x_test`): they are different
packages compiled into one test binary. A consuming corpus
(`candosa/cerebro`, `internal/compute/iva`) declared `mustPeriodicInput` in both — an
in-package fixture sealer and an external-package twin — and the freshness pass
refused the whole package as unmeasurable with an ambiguous-subject diagnosis. The
caller worked around it by renaming the internal helper (`sealedPeriodicInput`),
which is a naming constraint Go itself does not impose and other repositories will
trip on wherever internal/external test pairs share fixture vocabulary.

## Shape

The ambiguity indicates subject identity is resolved at (directory, identifier)
granularity. The compiler's own view — import path with the internal/external
test-package distinction — is unambiguous for every legal Go program, and the test
binary layout preserves it. Keying subjects on that identity removes the failure
class; a genuine collision inside one package remains impossible in compiled Go.
Until then the workaround is unique helper names across a directory's two test
packages, which nothing in the language or its tooling otherwise requires.
