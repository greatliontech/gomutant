package engine

import (
	"strings"

	"github.com/greatliontech/gofresh/gotool"
)

// SetEnvKey composes one key onto an environment: every entry naming
// key under the host platform's key rule (gotool.EqualEnvKey — folded
// on Windows, exact elsewhere) is dropped and one key=value entry
// appended, every other entry kept in order — an entry with no "="
// names nothing and is kept: it is not this rule's to sanitize, the
// ambient refusals (ambientEnvironmentRefused) having refused such an
// environment before any composer sees it, and a library caller
// skipping them meets gofresh's own refusal of the malformed entry at
// the first engine, fail-closed. It is the one rule every composer of a
// spawn environment reads, so a composed environment carries each key
// once whatever the ambient environment carried and the producer
// environment gofresh records never refuses a duplicate
// (REQ-exec-spawn-environment).
func SetEnvKey(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); !ok || !gotool.EqualEnvKey(name, key) {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}

// LookupEnvKey reports key's effective value in env — the last entry
// naming it under the platform's key rule, os/exec's duplicate-key
// resolution; an entry with no "=" names nothing — and whether any
// entry named it.
func LookupEnvKey(env []string, key string) (string, bool) {
	value, found := "", false
	for _, entry := range env {
		if name, rest, ok := strings.Cut(entry, "="); ok && gotool.EqualEnvKey(name, key) {
			value, found = rest, true
		}
	}
	return value, found
}
