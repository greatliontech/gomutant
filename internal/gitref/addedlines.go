package gitref

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/greatliontech/gomutant"
)

// ChangedSurface reads the changed surface against ref in one git
// question: the tree-relative changed paths (tracked changes plus
// untracked files, REQ-target-changed) and, for the Go files among
// them, the lines the working tree ADDED — git's line diff of the
// current content against the ref's, hunk by hunk (`@@ -a,b +c,d @@`:
// d lines from c are added; a pure deletion adds none), an untracked
// file whole. Only Go files can carry survivors, so only they are
// diffed and counted. The added-line surface is the delta geometry the
// faces cut open survivors by — advisory, never a pin.
func ChangedSurface(dir, ref string) (gomutant.ChangedSurface, error) {
	return ChangedSurfaceContext(context.Background(), dir, ref)
}

// ChangedSurfaceContext is ChangedSurface with caller-owned cancellation.
func ChangedSurfaceContext(ctx context.Context, dir, ref string) (gomutant.ChangedSurface, error) {
	surface := gomutant.ChangedSurface{Ref: ref, Added: map[string][]gomutant.LineRange{}}
	tracked, err := outputContext(ctx, dir, "-c", "core.quotepath=off", "diff", "--name-only", "--relative", ref)
	if err != nil {
		return surface, err
	}
	untracked, err := outputContext(ctx, dir, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return surface, err
	}
	seen := map[string]bool{}
	for _, out := range [][]byte{tracked, untracked} {
		if err := ctx.Err(); err != nil {
			return surface, err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" && !seen[line] {
				seen[line] = true
				surface.Paths = append(surface.Paths, line)
			}
		}
	}
	// The diff runs without a pathspec (a large changed surface would
	// exceed the argument limit) and is filtered to the Go paths the
	// name-only pass reported. The prefixes are forced and external
	// diff drivers disabled so the header grammar is the one parsed
	// whatever the operator's git configuration says, and renames are
	// not paired: changed-scope discovery reads a renamed file as wholly
	// new (its path has no content at the ref), so its added lines must
	// be the whole file too, not the hunks against the old path.
	diff, err := outputContext(ctx, dir, "-c", "core.quotepath=off", "diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", "--relative", ref)
	if err != nil {
		return surface, err
	}
	if err := parseUnifiedAdded(bytes.NewReader(diff), func(path string) bool { return seen[path] && isGo(path) }, surface.Added); err != nil {
		return surface, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(untracked)), "\n") {
		if line == "" || !isGo(line) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return surface, err
		}
		n, err := lineCount(filepath.Join(dir, line))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return surface, err
		}
		if n > 0 {
			surface.Added[line] = []gomutant.LineRange{{From: 1, To: n + 1}}
		}
	}
	return surface, nil
}

func isGo(path string) bool { return strings.HasSuffix(path, ".go") }

// parseUnifiedAdded folds a unified diff's added-line hunks into added
// for the files wanted admits. A file's path is read from the `+++ b/`
// line of its header, and only there: a header is the lines between a
// `diff --git` line and the file's first hunk, so an added CONTENT line
// that happens to start with `+++ ` (it lies after a hunk header) can
// never re-key the file. `+++ /dev/null` is a deletion and adds nothing.
func parseUnifiedAdded(diff io.Reader, wanted func(string) bool, added map[string][]gomutant.LineRange) error {
	reader := bufio.NewReader(diff)
	current, inHeader := "", false
	for {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			current, inHeader = "", true
		case inHeader && strings.HasPrefix(line, "+++ "):
			name := strings.TrimPrefix(line, "+++ ")
			if tab := strings.IndexByte(name, '\t'); tab >= 0 {
				name = name[:tab]
			}
			switch {
			case name == "/dev/null":
				current = ""
			case strings.HasPrefix(name, "b/"):
				current = strings.TrimPrefix(name, "b/")
			default:
				return fmt.Errorf("gitref: unexpected diff file header %q", line)
			}
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
			if current == "" || !wanted(current) {
				continue
			}
			from, n, err := parseHunkAdded(line)
			if err != nil {
				return err
			}
			if n > 0 {
				added[current] = append(added[current], gomutant.LineRange{From: from, To: from + n})
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

// parseHunkAdded reads the new-file side of a hunk header
// (`@@ -a[,b] +c[,d] @@ ...`): the start line and the count (1 when
// omitted, 0 for a pure deletion).
func parseHunkAdded(header string) (from, count int, err error) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, fmt.Errorf("gitref: malformed hunk header %q", header)
	}
	spec := strings.TrimPrefix(fields[2], "+")
	start, length, hasLength := strings.Cut(spec, ",")
	if from, err = strconv.Atoi(start); err != nil {
		return 0, 0, fmt.Errorf("gitref: malformed hunk header %q: %w", header, err)
	}
	count = 1
	if hasLength {
		if count, err = strconv.Atoi(length); err != nil {
			return 0, 0, fmt.Errorf("gitref: malformed hunk header %q: %w", header, err)
		}
	}
	return from, count, nil
}

// lineCount streams a file's line count the way positions count lines:
// a final unterminated line is a line.
func lineCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	n, last := 0, byte('\n')
	buf := make([]byte, 64*1024)
	for {
		k, err := reader.Read(buf)
		if k > 0 {
			n += bytes.Count(buf[:k], []byte("\n"))
			last = buf[k-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if last != '\n' {
		n++
	}
	return n, nil
}
