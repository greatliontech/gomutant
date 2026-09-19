package engine

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
)

// TestSetEnvKeyLeavesEachKeyOnce is the property behind every spawn
// environment gomutant composes: over arbitrary ambient environments —
// duplicated keys, case-varied keys, empty values — the composed key
// appears exactly once under the platform's rule, with the composed
// value, and every other entry survives in order; the lookup answers
// the last entry, os/exec's rule (REQ-exec-oracle-parallelism).
func TestSetEnvKeyLeavesEachKeyOnce(t *testing.T) {
	// The first caseVariants keys spell one variable in three cases —
	// the draws compose one of those, so the platform rule is exercised
	// against its own variants.
	const caseVariants = 3
	keys := []string{"GOMAXPROCS", "gomaxprocs", "GoMaxProcs", "GOMEMLIMIT", "PWD", "A", "B"}
	values := []string{"", "1", "x=y", "off"}
	rng := rand.New(rand.NewSource(246))
	for draw := 0; draw < 2000; draw++ {
		n := rng.Intn(7)
		env := make([]string, 0, n)
		for i := 0; i < n; i++ {
			env = append(env, keys[rng.Intn(len(keys))]+"="+values[rng.Intn(len(values))])
		}
		key := keys[rng.Intn(caseVariants)]
		value := fmt.Sprintf("v%d", draw)
		got := SetEnvKey(env, key, value)
		var others, matching []string
		for _, entry := range got {
			name, _, _ := strings.Cut(entry, "=")
			if gotool.EqualEnvKey(name, key) {
				matching = append(matching, entry)
			} else {
				others = append(others, entry)
			}
		}
		if len(matching) != 1 || matching[0] != key+"="+value || got[len(got)-1] != key+"="+value {
			t.Fatalf("draw %d: SetEnvKey(%v, %q, %q) = %v", draw, env, key, value, got)
		}
		var kept []string
		for _, entry := range env {
			name, _, _ := strings.Cut(entry, "=")
			if !gotool.EqualEnvKey(name, key) {
				kept = append(kept, entry)
			}
		}
		if !slices.Equal(others, kept) {
			t.Fatalf("draw %d: other entries moved: %v, want %v", draw, others, kept)
		}
		if v, ok := LookupEnvKey(got, key); !ok || v != value {
			t.Fatalf("draw %d: lookup after set = %q/%v", draw, v, ok)
		}
		if v, ok := LookupEnvKey(env, key); ok {
			// The lookup answers the LAST matching ambient entry.
			last := ""
			for _, entry := range env {
				name, rest, _ := strings.Cut(entry, "=")
				if gotool.EqualEnvKey(name, key) {
					last = rest
				}
			}
			if v != last {
				t.Fatalf("draw %d: lookup = %q, want the last entry %q", draw, v, last)
			}
		}
	}
	// The platform rule is the anchor: a case-varied key is the same
	// variable on Windows and another one elsewhere.
	got := SetEnvKey([]string{"gomaxprocs=2"}, "GOMAXPROCS", "4")
	if runtime.GOOS == "windows" {
		if !slices.Equal(got, []string{"GOMAXPROCS=4"}) {
			t.Fatalf("windows: %v", got)
		}
	} else if !slices.Equal(got, []string{"gomaxprocs=2", "GOMAXPROCS=4"}) {
		t.Fatalf("unix: %v", got)
	}
}

// TestGoEnvPinsTheLoaderDriverOff pins the loader's delegation off:
// whatever GOPACKAGESDRIVER the ambient environment carries, the
// environment every load and spawn runs under names the go command's
// own listing, once.
func TestGoEnvPinsTheLoaderDriverOff(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "/usr/bin/false")
	env := GoEnv(t.TempDir())
	n := 0
	for _, entry := range env {
		if name, value, _ := strings.Cut(entry, "="); name == "GOPACKAGESDRIVER" {
			n++
			if value != "off" {
				t.Fatalf("driver = %q, want off", value)
			}
		}
	}
	if n != 1 {
		t.Fatalf("GOPACKAGESDRIVER appears %d times in %v", n, env)
	}
	if v, ok := LookupEnvKey(env, "GOWORK"); !ok || v != "off" {
		t.Fatalf("a tree without go.work pins GOWORK=%q/%v, want off", v, ok)
	}
}
