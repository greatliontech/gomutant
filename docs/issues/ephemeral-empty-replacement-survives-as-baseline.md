# Reported empty-replacement discrepancy remains unreproduced

Lands: tooling-redesign 5.4.

The consumer reported that a batch edit whose `new_string` is empty — the
natural spelling of
"delete the guard whole", which the ephemeral guidance itself
prescribes over neutering — is accepted by the parser (`editbatch.go`
refuses an empty `old_string` and a byte-identical pair, and
requires the field present, not non-empty), and the probe then
reported `SURVIVED` naming a file the edit never touched. The same
deletion spelled with a comment line as the replacement was killed by
the same test. That discrepancy does not establish whether the replacement
was ignored, the wrong file was attributed, or another boundary failed.

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

A reported hand edit of the tree (the arm deleted, the test run, the file
restored) failed the same test the same way. The report therefore warrants a
reproduction with the exact source, edit, executable identity and attribution;
it does not establish the current primitive's behavior.

Two observations need separate reproduction: the different verdict for an
empty replacement and the result naming `store.go` for an edit to `types.go`.
Neither establishes which bytes the producing compiler actually received.

The reported working tree is on no branch of ocifs. A same-shape fixture
(a String switch arm deleted, with an error-message assertion) was historically
killed by both spellings under gomutant 58e0f41-era binaries. Current metadata
replacement handling distinguishes a nil replacement from a supplied source,
including the deletion case; there is no basis to diagnose an ignored empty
replacement from this report. Keep the consumer case open without claiming a
new execution or reproducer. Full-oracle@3 admission and actual mutant-file
attribution must both hold when it is reproduced.
