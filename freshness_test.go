package gomutant

import "testing"

// The package cut absorbs gopkg.in-style version elements and leaves
// method spellings alone: without the absorption a dark versioned
// package merged with its sibling and went unreported
// (REQ-result-skip-radius; the chunk-132 review's L2).
func TestSymbolPackageAbsorbsVersionElements(t *testing.T) {
	cases := map[string]string{
		"example.com/mod/pkg.Func":        "example.com/mod/pkg",
		"example.com/mod/pkg.Recv.Method": "example.com/mod/pkg",
		"gopkg.in/yaml.v3.Marshal":        "gopkg.in/yaml.v3",
		"gopkg.in/yaml.v3.Node.Decode":    "gopkg.in/yaml.v3",
		"example.com/mod.v12.sub.F":       "example.com/mod.v12",
		"pkg.F":                           "pkg",
	}
	for symbol, want := range cases {
		if got := symbolPackage(symbol); got != want {
			t.Fatalf("symbolPackage(%q) = %q, want %q", symbol, got, want)
		}
	}
}
