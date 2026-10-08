// Package gitcmd is the one home of every git command gomutant spawns
// — commit provenance and residue reads, the changed surface and ref
// content, the fixture guard's status, the repositories the fixture
// package builds — prepared through Gofresh's consumer-command form of its
// go-command policy (gotool.Runner.Program): the directory and the
// derived environment the policy gives, the containment's process
// group swept on a cancellation, the reap bounded by the policy's
// wait delay; a failure carries git's stderr and a cancellation is
// named as the caller's (REQ-exec-go-command-runner).
package gitcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/greatliontech/gofresh/gotool"
)

// runner is the one git runner: the policy's containment, no quit
// grace (a git child is killed outright with its group), no hook.
var runner = gotool.Runner{Containment: &gotool.Containment{}}

// Hermetic is env with every route from the host's git to a
// repository's configuration, hooks and location closed: the system
// and global configurations excluded (GIT_CONFIG_NOSYSTEM;
// GIT_CONFIG_GLOBAL at /dev/null — read by git 2.32 and later), the
// command-scope configuration dropped (GIT_CONFIG_PARAMETERS,
// GIT_CONFIG_COUNT and every GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n — a
// hook path or file-system monitor a wrapping process exported), the
// configuration file `git config` writes redirected by GIT_CONFIG
// dropped (the repository's own file otherwise never carries what the
// caller set), the hook template source dropped (GIT_TEMPLATE_DIR —
// `git init` copies its hooks into every repository), and the
// repository redirection dropped (GIT_DIR, GIT_WORK_TREE,
// GIT_INDEX_FILE, GIT_OBJECT_DIRECTORY,
// GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_COMMON_DIR, GIT_NAMESPACE — a
// test run from a hook would otherwise write into the hook's own
// repository); every entry of a dropped key goes, whatever its
// position. Outside the set: the identity and date variables
// (GIT_AUTHOR_*, GIT_COMMITTER_* — content, no route), discovery
// bounds (a repository at dir is found first), remote transport,
// editor and pager (the streams are buffers, the commits carry their
// message), and GIT_EXEC_PATH (git's own subcommand binaries, no
// repository semantics). The repository's own configuration is the
// caller's.
func Hermetic(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if hermeticDropped[key] || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, entry)
	}
	out = gotool.SetEnv(out, "GIT_CONFIG_NOSYSTEM", "1")
	return gotool.SetEnv(out, "GIT_CONFIG_GLOBAL", "/dev/null")
}

// hermeticDropped is the fixed set Hermetic drops; the indexed
// command-scope keys are dropped by prefix.
var hermeticDropped = map[string]bool{
	"GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true,
	"GIT_CONFIG": true, "GIT_TEMPLATE_DIR": true,
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR": true, "GIT_NAMESPACE": true,
}

// Prepare is git with args in dir under env, prepared and unstarted —
// the form a caller composing its own environment or streams runs
// itself (a fixture repository's hermetic git).
func Prepare(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	return runner.Program(ctx, dir, env, "git", args...)
}

// Output runs git with args in dir under the process's environment and
// answers its stdout; a failure names the command and carries git's
// stderr, and a cancellation is answered as the context's own error,
// never as the exit it caused.
func Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd, err := Prepare(ctx, dir, os.Environ(), args...)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
