# An ephemeral batch edit with an empty replacement survives as the baseline

Lands: a reproduction on a committed tree — the batch, the tree's revision, and the ephemeral verdict — since the reported tree (ocifs's `pullPolicyUnset` guard and `TestNewStoreRefusesUnnamedPullPolicy`) is on no branch of the repository, and a fixture of the reported shape (a String switch arm deleted with an empty replacement, the error-message test) is killed by both spellings under gomutant 58e0f41-era binaries

A batch edit whose `new_string` is empty — the natural spelling of
"delete the guard whole", which the ephemeral guidance itself
prescribes over neutering — is accepted by the parser (`editbatch.go`
refuses an empty `old_string` and a byte-identical pair, and
requires the field present, not non-empty), and the probe then
reports `SURVIVED` naming a file the edit never touched. The same
deletion spelled with a comment line as the replacement is killed by
the same test, so the survivor is the unmutated baseline, or a
mutant built from the wrong file, rendered as a verdict.

Field report (greatliontech/ocifs, gomutant
v0.57.11-0.20260919151557-ee9616fd4b7b), reproduced twice:

```
[{"file":"internal/store/types.go",
  "old_string":"\tcase pullPolicyUnset:\n\t\treturn \"Unset\"\n",
  "new_string":""}]
```

```
gomutant ephemeral --batch p3.json --test-pkg ./internal/store \
  --run TestNewStoreRefusesUnnamedPullPolicy
SURVIVED  internal/store/store.go  — TestNewStoreRefusesUnnamedPullPolicy did not notice the mutation
```

The same edit with `"new_string":"\t// probe\n"`:

```
killed    internal/store/types.go  by ...TestNewStoreRefusesUnnamedPullPolicy
      store_test.go:915: error "pull policy Unknown is not one of ...", want "pull policy Unset is not one of ..."
```

A hand edit of the tree (the arm deleted, the test run, the file
restored) fails the same test the same way, so the deletion is a
kill and the probe's `SURVIVED` is a false verdict — the one
outcome a probe must never render, since a false survivor reads as
a vacuous test and sends the operator strengthening a test that is
already load-bearing.

Two symptoms to separate at triage: the empty replacement not
applied (or applied and discarded), and the result naming
`store.go` for an edit scoped to `types.go` — the second may be the
first's trace (a mutant with no changed file attributed to the
package's first file).
