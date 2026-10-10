package testsuite

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
)

func TestMain(m *testing.M) {
	if os.Getenv("GOMUTANT_SUITE_CACHE_CHILD") == "1" {
		Main(m, filepath.Join("..", "engine", "testdata"), gotool.Runner{Containment: &gotool.Containment{}})
	}
	os.Exit(m.Run())
}

func TestSuiteCacheEnvironment(t *testing.T) {
	if os.Getenv("GOMUTANT_SUITE_CACHE_CHILD") == "1" {
		if got := os.Getenv("GOCACHE"); got != os.Getenv("GOMUTANT_SUITE_EXPECT_CACHE") {
			t.Fatalf("child cache=%q, expected %q", got, os.Getenv("GOMUTANT_SUITE_EXPECT_CACHE"))
		}
		if os.Getenv("XDG_CACHE_HOME") == os.Getenv("GOMUTANT_SUITE_ORIGINAL_HOME") {
			t.Fatal("tool cache was not isolated")
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"omitted", "off", "goenv-spaces"} {
		home := t.TempDir()
		env := gotool.SetEnv(gotool.UnsetEnv(os.Environ(), "GOCACHE"), "XDG_CACHE_HOME", home)
		env = gotool.SetEnv(env, "GOENV", "off")
		want := mode
		if mode == "goenv-spaces" {
			want = filepath.Join(home, "configured cache ")
			config := filepath.Join(home, "goenv")
			if err := os.WriteFile(config, []byte("GOCACHE="+want+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			env = gotool.SetEnv(env, "GOENV", config)
		}
		if mode != "off" {
			probe := exec.CommandContext(t.Context(), "go", "env", "GOCACHE")
			probe.Env = env
			out, err := probe.Output()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "goenv-spaces" && string(out) != want+"\n" {
				t.Fatalf("Go changed the configured path: %q", out)
			}
			want = strings.TrimSuffix(string(out), "\n")
			if want == "" {
				t.Fatal("independent go cache probe was empty")
			}
		} else {
			env = gotool.SetEnv(env, "GOCACHE", mode)
		}
		env = gotool.SetEnv(env, "GOMUTANT_SUITE_CACHE_CHILD", "1")
		env = gotool.SetEnv(env, "GOMUTANT_SUITE_EXPECT_CACHE", want)
		env = gotool.SetEnv(env, "GOMUTANT_SUITE_ORIGINAL_HOME", home)
		child := exec.CommandContext(t.Context(), self, "-test.run=^TestSuiteCacheEnvironment$", "-test.count=1")
		child.Env = env
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("cache %q: %v\n%s", mode, err, out)
		}
	}
}

func TestBuildCacheSurvivesToolCacheIsolation(t *testing.T) {
	for _, configured := range []string{"", "off", filepath.Join(t.TempDir(), "explicit")} {
		t.Run(configured, func(t *testing.T) {
			original := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", original)
			t.Setenv("GOCACHE", configured)
			calls := 0
			resolved := filepath.Join(original, "go-build")
			if err := preserveBuildCache(func() (string, error) {
				calls++
				if os.Getenv("XDG_CACHE_HOME") != original {
					t.Fatal("cache queried after isolation")
				}
				return resolved + "\n", nil
			}); err != nil {
				t.Fatal(err)
			}
			want := configured
			if want == "" {
				want = resolved
			}
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			if got := os.Getenv("GOCACHE"); got != want {
				t.Fatalf("cache=%q, want %q", got, want)
			}
			if configured == "" && calls != 1 || configured != "" && calls != 0 {
				t.Fatalf("queries=%d configured=%q", calls, configured)
			}
		})
	}
}

func TestBuildCacheResolutionRefusesWithoutASetting(t *testing.T) {
	errProbe := errors.New("sample refused")
	for _, tc := range []struct {
		value string
		err   error
	}{{"", nil}, {" \n", nil}, {"", errProbe}} {
		t.Setenv("GOCACHE", "")
		err := preserveBuildCache(func() (string, error) { return tc.value, tc.err })
		if err == nil || tc.err != nil && !errors.Is(err, tc.err) {
			t.Fatalf("resolution=%q error=%v", tc.value, err)
		}
		if os.Getenv("GOCACHE") != "" {
			t.Fatal("failed resolution installed a cache")
		}
	}
}
