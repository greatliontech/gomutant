package engine

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Selection is a run's declared build selection: build tags and a
// toolchain directive. The zero value selects nothing and leaves the
// ambient environment untouched — the pre-surface behavior exactly.
// A declared selection rewrites the tree's one frozen environment at
// load (GOFLAGS's -tags flag and GOTOOLCHAIN), so every consumer of
// that environment — package loading, target discovery, constraint
// matching, oracle spawns, the measurement pins — sees the same
// selection by construction; declared tags replace any ambient -tags
// rather than merging, because a silent union would measure a
// selection nobody named.
type Selection struct {
	Tags      []string
	Toolchain string
}

// Declared reports whether the selection names anything.
func (s Selection) Declared() bool { return len(s.Tags) > 0 || s.Toolchain != "" }

// applyEnv rewrites one frozen environment under the selection,
// validating the declared values: a malformed tag or toolchain refuses
// before any load, never a silently ignored constraint.
func (s Selection) applyEnv(env []string) ([]string, error) {
	if !s.Declared() {
		return env, nil
	}
	for _, tag := range s.Tags {
		if !validBuildTag(tag) {
			return nil, fmt.Errorf("gomutant: build tag %q is not a valid constraint tag", tag)
		}
	}
	if s.Toolchain != "" && !validToolchain(s.Toolchain) {
		return nil, fmt.Errorf("gomutant: toolchain %q is not a valid GOTOOLCHAIN directive", s.Toolchain)
	}
	out := make([]string, 0, len(env)+2)
	var goflags string
	seenFlags := false
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		// Case-insensitive name matching follows the Windows
		// environment convention, exactly as GoEnv treats GOWORK.
		switch {
		case strings.EqualFold(name, "GOFLAGS"):
			goflags, seenFlags = value, true
		case strings.EqualFold(name, "GOTOOLCHAIN") && s.Toolchain != "":
			// Replaced below.
		default:
			out = append(out, entry)
		}
	}
	if s.Toolchain != "" {
		out = append(out, "GOTOOLCHAIN="+s.Toolchain)
	}
	if len(s.Tags) > 0 {
		goflags = replaceTagsFlag(goflags, strings.Join(s.CanonicalTags(), ","))
		out = append(out, "GOFLAGS="+goflags)
	} else if seenFlags {
		out = append(out, "GOFLAGS="+goflags)
	}
	return out, nil
}

// CanonicalTags is the declared tag set sorted and deduplicated: tag
// order is presentation, never a distinct selection, so the effective
// GOFLAGS, the measurement pins, and any selection-keyed cache all
// compare one canonical form.
func (s Selection) CanonicalTags() []string {
	out := append([]string(nil), s.Tags...)
	sort.Strings(out)
	return slices.Compact(out)
}

// Validate refuses a malformed selection without touching any
// environment: the boundary check selection-keyed consumers run before
// deriving identity from the declaration, so a malformed declaration
// refuses identically whether a cache is cold or warm.
func (s Selection) Validate() error {
	_, err := s.applyEnv(nil)
	return err
}

// replaceTagsFlag rewrites a GOFLAGS value so its one effective -tags
// flag (single- or double-dash, last-wins as the go command resolves a
// repeat) is the declared set; other flags pass through untouched.
func replaceTagsFlag(goflags, tags string) string {
	fields := strings.Fields(goflags)
	kept := fields[:0]
	for _, flag := range fields {
		if _, ok := strings.CutPrefix(strings.TrimLeft(flag, "-"), "tags="); ok {
			continue
		}
		kept = append(kept, flag)
	}
	return strings.Join(append(kept, "-tags="+tags), " ")
}

// validBuildTag admits exactly the go command's constraint-tag alphabet:
// letters, digits, underscores, and dots, non-empty, with an optional
// leading "!" refused — negation belongs in constraint expressions, not
// selections.
func validBuildTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, r := range tag {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// validToolchain refuses only whitespace and control characters: the
// go command is the authority on directive shapes ("auto", "local",
// versions, custom toolchain names), and its own refusal of an unknown
// directive is surfaced by the load-time version probe with go's
// stderr attached.
func validToolchain(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
