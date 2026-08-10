package engine

import (
	"bytes"
	"testing"
)

// The site window discriminates same-shaped mutation sites: two
// byte-identical expressions with different neighboring lines hash
// apart - the neighbor shifted into old coordinates by an edit never
// matches - while the hash is stable for the same site
// (REQ-attest-survivor).
func TestSiteHashDiscriminatesSameShapedSites(t *testing.T) {
	source := []byte("package p\n\nfunc A(a, b int) bool {\n\tif a > b {\n\t\treturn true\n\t}\n\treturn false\n}\n\nfunc B(a, b int) bool {\n\tif a > b {\n\t\treturn hiddenElsewhere(a)\n\t}\n\treturn false\n}\n")
	first := bytes.Index(source, []byte("a > b"))
	second := bytes.LastIndex(source, []byte("a > b"))
	if first == second {
		t.Fatal("fixture needs two same-shaped sites")
	}
	h1 := siteHash(source, first, first+len("a > b"))
	h2 := siteHash(source, second, second+len("a > b"))
	if h1 == h2 {
		t.Fatalf("same-shaped sites with different neighbors collided: %s", h1)
	}
	if h1 != siteHash(source, first, first+len("a > b")) {
		t.Fatal("site hash is not stable")
	}

	// The window spans the enclosing line plus one line each side: an
	// edit outside that window leaves the anchor untouched.
	moved := bytes.Replace(source, []byte("return false\n}\n\nfunc B"), []byte("return false // trailing note\n}\n\nfunc B"), 1)
	if siteHash(moved, first, first+len("a > b")) != h1 {
		t.Fatal("an edit outside the window moved the anchor")
	}

	// Bounds: a window at the file edges clamps without panicking.
	if siteHash(source, 0, 1) == "" || siteHash(source, len(source)-1, len(source)) == "" {
		t.Fatal("edge windows produced empty hashes")
	}
}
