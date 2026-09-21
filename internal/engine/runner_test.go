package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

// spawnLog records the go commands the tree's runner prepared while an
// observer stood: each as its directory and arguments.
type spawnLog struct {
	dirs [][]string
}

func (l *spawnLog) observe(cmd *exec.Cmd) {
	dir, _ := filepath.Abs(cmd.Dir)
	l.dirs = append(l.dirs, append([]string{dir}, cmd.Args[1:]...))
}

// sawArg reports whether a command ran in dir carrying arg anywhere in
// its arguments.
func (l *spawnLog) sawArg(dir string, arg string) bool {
	want, _ := filepath.Abs(dir)
	for _, spawn := range l.dirs {
		if spawn[0] == want && slices.Contains(spawn[1:], arg) {
			return true
		}
	}
	return false
}

// saw reports whether a command ran in dir with args as its leading
// arguments.
func (l *spawnLog) saw(dir string, args ...string) bool {
	want, _ := filepath.Abs(dir)
	for _, spawn := range l.dirs {
		if spawn[0] != want || len(spawn) < len(args)+1 {
			continue
		}
		if strings.Join(spawn[1:len(args)+1], " ") == strings.Join(args, " ") {
			return true
		}
	}
	return false
}

func (l *spawnLog) String() string {
	var b strings.Builder
	for _, spawn := range l.dirs {
		b.WriteString(strings.Join(spawn, " ") + "\n")
	}
	return b.String()
}

// TestTreeGoCommandsRideTheRunner pins the wiring of every go command
// the engine package spawns to the tree's runner
// (REQ-exec-go-command-runner): the load's toolchain sample in the
// tree directory, the linked-set listing and the build-configuration
// snapshot in the tree directory, under an observed probe the
// oracle's `go test` in the tree directory and the ingest's roots
// probe (`go env`) in the package directory, and the coverage probe
// and a mutant run in the tree directory. The probe's environment
// carries a key of its own: gofresh memoizes the roots per (package
// directory, environment) for the process, so an earlier test's probe
// would otherwise answer the roots without a spawn.
func TestTreeGoCommandsRideTheRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and probes the fixture tree")
	}
	var log spawnLog
	defer ObserveGoCommandsForTest(log.observe)()
	ctx := context.Background()
	tr := fixtureTree(t)
	if !log.saw("testdata/fixturemod", "env", "GOVERSION") {
		t.Fatalf("the load's toolchain sample did not ride the runner:\n%s", &log)
	}
	if _, err := tr.LinkedTestPackagesContext(ctx, "example.com/fixture/lib"); err != nil {
		t.Fatal(err)
	}
	if !log.saw("testdata/fixturemod", "list", "-deps", "-test") {
		t.Fatalf("the linked-set listing did not ride the runner:\n%s", &log)
	}
	if _, err := tr.buildMatchContext(ctx, tr.dir); err != nil {
		t.Fatal(err)
	}
	if !log.saw(tr.dir, "env", "-json") {
		t.Fatalf("the build-configuration snapshot did not ride the runner:\n%s", &log)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	log.dirs = nil
	env := gotool.SetEnv(GoEnv("testdata/fixturemod"), "GOMUTANT_RUNNER_PIN", t.Name())
	ran, passed, _, _, state, err := TestProbeObservedEnv(ctx, "testdata/fixturemod", "example.com/fixture/lib", "^TestPickInput$", time.Minute, nil, moduleDir, packageDir, nil, nil, env, OracleBounds{})
	if err != nil || ran != 1 || !passed || !state.OK {
		t.Fatalf("observed probe: ran=%d passed=%v ok=%v err=%v", ran, passed, state.OK, err)
	}
	if !log.saw("testdata/fixturemod", "test") {
		t.Fatalf("the oracle did not ride the runner:\n%s", &log)
	}
	if !log.saw(packageDir, "env") {
		t.Fatalf("the ingest's roots probe did not ride the runner:\n%s", &log)
	}
	log.dirs = nil
	if _, err := CoveredPositions(ctx, "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", "example.com/fixture/lib", time.Minute, nil, env, tr.DirectiveCoverage(), OracleBounds{}); err != nil {
		t.Fatal(err)
	}
	if !log.sawArg("testdata/fixturemod", "-coverpkg") {
		t.Fatalf("the coverage probe did not ride the runner:\n%s", &log)
	}
	ms, err := tr.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil || len(ms) == 0 {
		t.Fatalf("mutants of Add: %d, %v", len(ms), err)
	}
	log.dirs = nil
	if _, _, _, err := RunMutantEnv(ctx, "testdata/fixturemod", ms[0], []string{"example.com/fixture/lib"}, "^TestAdd$", time.Minute, nil, env, OracleBounds{}); err != nil {
		t.Fatal(err)
	}
	if !log.sawArg("testdata/fixturemod", "-overlay") {
		t.Fatalf("the mutant run did not ride the runner:\n%s", &log)
	}
}

// TestListingServesTheAnswerAWaitDelayLeaves pins the listing's salvage
// (REQ-exec-go-command-runner): a go wrapper whose housekeeping child
// outlives the answer holds the listing's pipe past the policy's wait
// delay; the listing exited cleanly with its answer written, and the
// answer serves. The wrapper stands first on PATH for a fresh load of
// the fixture tree, so every go child of the load meets it too (the
// toolchain sample serves its first line by gofresh's rule; the
// loader's listings read to the pipe's end).
func TestListingServesTheAnswerAWaitDelayLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree under a go wrapper")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper is a shell script")
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	script := "#!/bin/sh\n\"" + goBinary + "\" \"$@\"\nstatus=$?\n( sleep 2 ) &\nexit $status\n"
	if err := os.WriteFile(filepath.Join(shim, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := context.Background()
	tr, err := loadContext(ctx, "testdata/fixturemod", Selection{}, true)
	if err != nil {
		t.Fatalf("load under the wrapper: %v", err)
	}
	set, err := tr.LinkedTestPackagesContext(ctx, "example.com/fixture/lib")
	if err != nil {
		t.Fatalf("the listing under a pipe-holding wrapper refused: %v", err)
	}
	if !set["example.com/fixture/lib"] {
		t.Fatalf("the served listing lacks the package itself: %v", set)
	}
}
