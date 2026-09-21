# A package-crash kill's output head omits the panic

Lands: cross-tool train chunk 267 (the run's grammars one source on both faces — the package-crash kill carries the panic's bounded head)

An ephemeral probe whose mutant panics outside any test goroutine
is attributed, per REQ-exec-attribution, as a package-scope failure
with no test-level event. The kill line carries an output head of
one line, the package's `FAIL` summary; the panic itself — its
message and the frame that raised it — is not shown, so the operator
sees a kill and not what died. A test-attributed kill carries the
killing test's bounded output head; the package-crash arm would
serve the same purpose by carrying the panic's head.

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
