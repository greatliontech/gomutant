package integrationtest

import (
	"os"
	"strings"
	"testing"
)

// Pin fails t unless the running suite took the selection's default:
// under the default selection the binary runs -short, under the
// integration selection it does not, and in either an explicit
// -test.short on the command line is the word that stands. Every suite
// whose TestMain calls DefaultToShort pins itself with it, so a TestMain
// that stops calling it fails its own package.
func Pin(t testing.TB) {
	t.Helper()
	if problem := Verdict(ExplicitShort(os.Args[1:]), Enabled, testing.Short()); problem != "" {
		t.Fatal(problem)
	}
}

// ExplicitShort reports whether the command line names -test.short —
// the flag's own spelling with one or two dashes, bare or with a value.
// The command line is read, never flag.Visit: DefaultToShort's own Set
// would report as a visit and blind the pin to a wrong default.
func ExplicitShort(args []string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if name == "test.short" || strings.HasPrefix(name, "test.short=") {
			return true
		}
	}
	return false
}

// Verdict is the pin's judgment over the three facts it reads: the
// command line's word stands whatever the selection; otherwise the
// default selection must run -short and the integration selection must
// not. Empty is a pass; the text names the fault.
func Verdict(explicit, enabled, short bool) string {
	switch {
	case explicit:
		return ""
	case !enabled && !short:
		return "default selection: the suite is not running -short; its TestMain does not call integrationtest.DefaultToShort"
	case enabled && short:
		return "integration selection: -short set without the command line"
	}
	return ""
}
