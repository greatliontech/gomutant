package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
)

func TestOracleObservationBindsTheDeliveredMemoryEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a resource-bounded oracle process")
	}
	dir := t.TempDir()
	bounds := OracleBounds{MemoryBytes: 3 << 30, Width: 1}
	soft := bounds.MemoryBytes - bounds.MemoryBytes/10
	for name, contents := range map[string]string{
		"go.mod": "module example.com/memoryenv\n\ngo 1.26\n",
		"memory_test.go": fmt.Sprintf(`package memoryenv
import ("os"; "testing")
func TestMemory(t *testing.T) {
 if got := os.Getenv("GOMEMLIMIT"); got != %q { t.Fatalf("memory environment = %%q", got) }
 if os.Getenv("TMPDIR") == "" { t.Fatal("missing delivered scratch directory") }
}
`, fmt.Sprint(soft)),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The parent value is deliberately different. The child asserts the
	// delivered override itself, so a matching parent guard is not evidence.
	env := gotool.SetEnv(GoEnv(dir), "GOMEMLIMIT", "512MiB")
	var deliveredEnv []string
	restore := ObserveGoCommandsForTest(func(cmd *exec.Cmd) {
		if len(cmd.Args) > 1 && cmd.Args[1] == "test" {
			deliveredEnv = slices.Clone(cmd.Env)
		}
	})
	defer restore()
	ran, passed, _, _, observation, err := TestProbeObservedEnv(context.Background(), dir, "example.com/memoryenv", "^TestMemory$", time.Minute, nil, dir, dir, nil, nil, env, bounds)
	if err != nil || ran != 1 || !passed || !observation.OK || observation.Unverifiable {
		t.Fatalf("baseline = ran %d passed %v observation %+v error %v", ran, passed, observation, err)
	}
	if len(deliveredEnv) == 0 {
		t.Fatal("the real oracle spawn was not observed")
	}
	delivered, err := runtimeinput.Current(context.Background(), observation.Manifest, dir, deliveredEnv)
	if err != nil || delivered.Digest != observation.Digest {
		t.Fatalf("observation bound the parent environment instead of the delivered value: recorded=%+v delivered=%+v err=%v", observation.State, delivered, err)
	}
	assertIdentityOnly(t, observation)
}

func assertIdentityOnly(t *testing.T, observation runtimeinput.Observation) {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(observation.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["outcome"] != nil || fields["subjects"] != nil {
		t.Fatalf("identity guards acquired unsupported outcomes: %s", data)
	}
}

func FuzzOracleResourceEnvironmentIsIdempotent(f *testing.F) {
	f.Add(int64(3<<30), 4, 2)
	f.Add(int64(0), 0, 8)
	f.Add(int64(-1), -1, -1)
	f.Fuzz(func(t *testing.T, memory int64, width, ambientWidth int) {
		env := []string{"GOMEMLIMIT=512MiB", "GOMAXPROCS=" + strconv.Itoa(ambientWidth), "UNRELATED=kept"}
		bounds := OracleBounds{MemoryBytes: memory, Width: width}
		once := OracleEvidenceEnv(env, bounds)
		if twice := OracleEvidenceEnv(once, bounds); !slices.Equal(once, twice) {
			t.Fatalf("reapplying resource bounds changed the environment: %v -> %v", once, twice)
		}
		if value, _ := gotool.LookupEnv(once, "UNRELATED"); value != "kept" {
			t.Fatal("resource composition altered an unrelated variable")
		}
		if memory <= 0 {
			if value, _ := gotool.LookupEnv(once, "GOMEMLIMIT"); value != "512MiB" {
				t.Fatal("unbounded memory erased the inherited limit")
			}
		}
		if width > 0 && ambientWidth > 0 && ambientWidth <= width {
			if value, _ := gotool.LookupEnv(once, "GOMAXPROCS"); value != strconv.Itoa(ambientWidth) {
				t.Fatal("resource composition widened the inherited width")
			}
		}
	})
}

func TestBaselineCompletionDistinguishesFailureFromAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("executes baseline completion variants")
	}
	for _, tc := range []struct {
		name, source string
		incomplete   bool
	}{
		{"ordinary failure", `package completion
import "testing"
func TestX(t *testing.T) { t.Fatal("ordinary failure") }
`, false},
		{"panic after passing harness", `package completion
import "testing"
func TestX(t *testing.T) {}
func TestMain(m *testing.M) { m.Run(); panic("after harness") }
`, true},
		{"panic after failing harness", `package completion
import "testing"
func TestX(t *testing.T) { t.Fail() }
func TestMain(m *testing.M) { m.Run(); panic("after harness") }
`, true},
		{"fatal after failing harness", `package completion
import ("testing"; "fmt"; "os")
func TestX(t *testing.T) { t.Fail() }
func TestMain(m *testing.M) { m.Run(); fmt.Println("fatal error: runtime aborted"); os.Exit(2) }
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"go.mod": "module example.com/completion\n\ngo 1.26\n", "completion_test.go": tc.source} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ran, passed, _, _, observation, err := TestProbeObservedEnv(context.Background(), dir, "example.com/completion", "^TestX$", time.Minute, nil, dir, dir, nil, nil, GoEnv(dir), OracleBounds{})
			if err != nil || ran != 1 || passed || !observation.OK {
				t.Fatalf("baseline ran=%d passed=%v observation=%+v err=%v", ran, passed, observation, err)
			}
			if observation.Unverifiable != tc.incomplete {
				t.Fatalf("completion disposition=%+v, want incomplete=%v", observation, tc.incomplete)
			}
			if tc.incomplete && !strings.Contains(observation.Reason, "before observation finalization") {
				t.Fatalf("missing abnormal-completion reason: %q", observation.Reason)
			}
			assertIdentityOnly(t, observation)
		})
	}
}
