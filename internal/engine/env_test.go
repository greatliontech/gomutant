package engine

import (
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
)

// TestComposedKeysReplaceTheAmbientEntryOnce is the property behind
// every spawn environment gomutant composes: over arbitrary
// duplicate-free ambient environments — case-varied keys, empty
// values, unrelated entries — each composer (the width, the memory
// ceiling, the oracle's scratch temp directory, the toolchain
// selection and the tags' flags, the ingest mirror's working
// directory) leaves its key exactly once under the platform's rule,
// with the composed value, every other entry kept in its order; a
// draw carrying a duplicated key is refused at preparation instead,
// never composed around (REQ-exec-spawn-environment,
// REQ-exec-oracle-parallelism). The setter's own order is gofresh's
// contract (gotool's TestSetEnvKeepsNormalizeEnvsOrder).
func TestComposedKeysReplaceTheAmbientEntryOnce(t *testing.T) {
	keys := []string{"GOMAXPROCS", "gomaxprocs", "GOMEMLIMIT", "GOWORK", "GOPACKAGESDRIVER", "GOTOOLCHAIN", "GOFLAGS", "PWD", "TMPDIR", "tmpdir", "A", "B"}
	values := []string{"", "1", "x=y", "off", "-tags=a"}
	composers := []struct {
		key     string
		compose func([]string) []string
	}{
		{"GOMAXPROCS", func(env []string) []string { return oracleCPUEnv(env, 1) }},
		{"GOMEMLIMIT", func(env []string) []string { return oracleMemoryEnv(env, 1<<30) }},
		{"GOTOOLCHAIN", func(env []string) []string {
			out, err := Selection{Toolchain: "go1.27.0"}.applyEnv(env)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}},
		{"GOFLAGS", func(env []string) []string {
			out, err := Selection{Tags: []string{"b"}}.applyEnv(env)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}},
		{"PWD", func(env []string) []string {
			return oracleIngestEnv(env, runtimeinput.ProducerFrame{PkgDir: "/pkg"}, OracleBounds{})
		}},
		{"TMPDIR", func(env []string) []string {
			out, _, _, remove, err := oracleScratch(env)
			if err != nil {
				t.Fatal(err)
			}
			remove()
			return out
		}},
	}
	rng := rand.New(rand.NewSource(246))
	for draw := 0; draw < 2000; draw++ {
		n := rng.Intn(7)
		env := make([]string, 0, n)
		for i := 0; i < n; i++ {
			env = append(env, keys[rng.Intn(len(keys))]+"="+values[rng.Intn(len(values))])
		}
		if duplicated(env) {
			if err := ambientEnvironmentRefused(env); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("draw %d: a duplicated key %v composed around: %v", draw, env, err)
			}
			continue
		}
		for _, c := range composers {
			got := c.compose(env)
			var others, matching []string
			for _, entry := range got {
				if name, _, _ := strings.Cut(entry, "="); gotool.EqualEnvKey(name, c.key) {
					matching = append(matching, entry)
				} else {
					others = append(others, entry)
				}
			}
			if len(matching) != 1 {
				t.Fatalf("draw %d: %s over %v composed %v", draw, c.key, env, got)
			}
			var kept []string
			for _, entry := range env {
				if name, _, _ := strings.Cut(entry, "="); !gotool.EqualEnvKey(name, c.key) {
					kept = append(kept, entry)
				}
			}
			if !slices.Equal(others, kept) {
				t.Fatalf("draw %d: %s over %v moved other entries: %v, want %v", draw, c.key, env, got, kept)
			}
		}
	}
	// The composed values themselves, and the platform rule as the
	// anchor: a case-varied key is the same variable on Windows and
	// another one elsewhere (TestOracleCPUEnv's lowercase anchor).
	if v, ok := gotool.LookupEnv(oracleMemoryEnv(nil, 1000), "GOMEMLIMIT"); !ok || v != "900" {
		t.Fatalf("GOMEMLIMIT = %q/%v, want the soft ceiling", v, ok)
	}
	if v, ok := gotool.LookupEnv(oracleIngestEnv([]string{"PWD=/elsewhere"}, runtimeinput.ProducerFrame{PkgDir: "/pkg"}, OracleBounds{}), "PWD"); !ok || v != "/pkg" {
		t.Fatalf("PWD = %q/%v, want the frame's package directory", v, ok)
	}
}

// duplicated reports whether env names one variable twice under the
// platform's rule.
func duplicated(env []string) bool {
	for i, a := range env {
		nameA, _, _ := strings.Cut(a, "=")
		for _, b := range env[:i] {
			if nameB, _, _ := strings.Cut(b, "="); gotool.EqualEnvKey(nameA, nameB) {
				return true
			}
		}
	}
	return false
}

// TestGoEnvPinsTheLoaderDriverOff pins the loader's delegation off and
// the workspace to the tree's: whatever GOPACKAGESDRIVER and GOWORK the
// ambient environment carries, the environment every load and spawn
// runs under names the go command's own listing and the tree's own
// workspace (off for a tree without go.work), each once.
func TestGoEnvPinsTheLoaderDriverOff(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "/usr/bin/false")
	t.Setenv("GOWORK", "/elsewhere/go.work")
	env := GoEnv(t.TempDir())
	for key, want := range map[string]string{"GOPACKAGESDRIVER": "off", "GOWORK": "off"} {
		n := 0
		for _, entry := range env {
			if name, value, _ := strings.Cut(entry, "="); name == key {
				n++
				if value != want {
					t.Fatalf("%s = %q, want %s", key, value, want)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s appears %d times in %v", key, n, env)
		}
	}
}
