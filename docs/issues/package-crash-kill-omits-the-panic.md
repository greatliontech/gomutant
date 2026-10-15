# Reproduce the reported missing panic on a package-crash kill

Lands: tooling-redesign 8.3.

The historical probe below reported a package-scope kill carrying only the
package's `FAIL` summary, without the panic message and frame. That is the
observed report, not a general description of current rendering. The current
`outputTail` path selects package-level events with `Test == ""`; its existing
synthetic test retains an unnamed panic event. The remaining investigation
needs the real crash stream's event attribution and truncation behavior,
including panic output associated with a named test.

Field report (greatliontech/gitprov at e51aee8, first seen on the
build installed before 9881159, reproduced on
v0.57.11-0.20260920055743-9881159d78c3):

```
[{"file":"sigstoretest/sigstoretest.go",
  "old_string":"\tif !ok {\n\t\tt.Fatalf(\"sigstoretest: the leaf's key is %T, not ECDSA\", leaf.PublicKey)\n\t}",
  "new_string":"\t_ = ok"}]
```

```
gomutant ephemeral --batch probe.json --test-pkg ./sigstoretest \
  --run 'TestSignedTagRefuses$'
killed    sigstoretest/sigstoretest.go  by (package failure: github.com/greatliontech/gitprov/sigstoretest)
  FAIL	github.com/greatliontech/gitprov/sigstoretest	0.012s
```

Under the mutant a nil `*ecdsa.PublicKey` receives `Equal` in a
goroutine the test's recorder helper started, so the process dies
with a nil-pointer panic and no `--- FAIL` line: the package-scope
attribution is right. What is missing is the panic text the test
binary wrote before dying (`panic: runtime error: invalid memory
address or nil pointer dereference` and the `sigstoretest.go` frame),
which a hand run of the same test shows and which would tell the
operator the kill is the guard's absence rather than an assertion.
The same probe with the message changed instead of the guard removed
is attributed to the test with its assertion line, so the gap is the
crash arm's alone.

Reproduce the supplied failure under the current package-crash attribution path
and preserve its bounded panic message and frame, without weakening the confirming baseline or treating corrupt
output as a pass. The current stream readers still need a consistent distinction
between build rejection, malformed data and a crash-truncated stream.

Secondary constituent: tooling-redesign 5.4 retains charter 269's live
stream-decoder obligation under REQ-core-attributed-kills and REQ-exec-attribution.
It does not retain the removed coverage-schedule producer or commission a
unit-build breaker that retries or skips its historical batches.
