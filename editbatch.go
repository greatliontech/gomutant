package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// BatchEdit is one exact replacement in an atomic manual-mutant edit batch.
// File is a canonical tree-relative slash path. Every edit in a batch resolves
// against the same original file contents.
type BatchEdit struct {
	File      string `json:"file"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// ParseEditBatch parses the CLI's strict JSON edit-batch document.
func ParseEditBatch(data []byte) ([]BatchEdit, error) {
	fields, err := decodeKnownObject(data, map[string]bool{"edits": true})
	if err != nil {
		return nil, fmt.Errorf("gomutant: parse edit batch: %w", err)
	}
	var strict struct {
		Edits []json.RawMessage `json:"edits"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&strict); err != nil {
		return nil, fmt.Errorf("gomutant: parse edit batch: %w", err)
	}
	if _, ok := fields["edits"]; !ok || isJSONNull(fields["edits"]) || len(strict.Edits) == 0 {
		return nil, fmt.Errorf("gomutant: edit batch is empty")
	}
	edits := make([]BatchEdit, len(strict.Edits))
	known := map[string]bool{"file": true, "old_string": true, "new_string": true}
	for i, raw := range strict.Edits {
		entryFields, err := decodeKnownObject(raw, known)
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse edit batch entry %d: %w", i, err)
		}
		for _, name := range []string{"file", "old_string", "new_string"} {
			value, ok := entryFields[name]
			if !ok || isJSONNull(value) {
				return nil, fmt.Errorf("gomutant: parse edit batch entry %d: missing field %s", i, name)
			}
		}
		entryDec := json.NewDecoder(bytes.NewReader(raw))
		entryDec.DisallowUnknownFields()
		if err := entryDec.Decode(&edits[i]); err != nil {
			return nil, fmt.Errorf("gomutant: parse edit batch entry %d: %w", i, err)
		}
	}
	return edits, nil
}

type fileReplacement struct {
	File   string
	Abs    string
	Source []byte
}

type resolvedEdit struct {
	start int
	end   int
	new   []byte
	index int
}

// prepareEditBatch resolves and applies an atomic edit batch in memory. It
// never writes the tree; the returned full-file replacements are ready for a
// later overlay run.
func prepareEditBatch(root string, edits []BatchEdit) ([]fileReplacement, error) {
	return prepareEditBatchContext(context.Background(), root, edits)
}

func prepareEditBatchContext(ctx context.Context, root string, edits []BatchEdit) ([]fileReplacement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("gomutant: edit batch is empty")
	}
	type fileEdits struct {
		file    string
		abs     string
		source  []byte
		entries []resolvedEdit
	}
	byAbs := map[string]*fileEdits{}
	for i, edit := range edits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if edit.OldString == "" {
			return nil, fmt.Errorf("gomutant: batch edit %d has an empty match", i+1)
		}
		if edit.OldString == edit.NewString {
			return nil, fmt.Errorf("gomutant: batch edit %d is byte-identical", i+1)
		}
		abs, err := resolveTreeFile(root, edit.File)
		if err != nil {
			return nil, fmt.Errorf("gomutant: batch edit %d: %w", i+1, err)
		}
		group := byAbs[abs]
		if group == nil {
			source, err := readFileContext(ctx, abs)
			if err != nil {
				return nil, fmt.Errorf("gomutant: read %s: %w", edit.File, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			group = &fileEdits{file: edit.File, abs: abs, source: source}
			byAbs[abs] = group
		} else if group.file != edit.File {
			return nil, fmt.Errorf("gomutant: %q and %q resolve to the same file", group.file, edit.File)
		}

		old := []byte(edit.OldString)
		switch count := overlappingMatchStarts(string(group.source), string(old)); count {
		case 0:
			return nil, fmt.Errorf("gomutant: batch edit %d matches nothing in %s", i+1, edit.File)
		case 1:
			start := bytes.Index(group.source, old)
			group.entries = append(group.entries, resolvedEdit{start: start, end: start + len(old), new: []byte(edit.NewString), index: i + 1})
		default:
			return nil, fmt.Errorf("gomutant: batch edit %d is ambiguous in %s (%d matches)", i+1, edit.File, count)
		}
	}

	replacements := make([]fileReplacement, 0, len(byAbs))
	for _, group := range byAbs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.Slice(group.entries, func(i, j int) bool { return group.entries[i].start < group.entries[j].start })
		var out bytes.Buffer
		cursor := 0
		for _, edit := range group.entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if edit.start < cursor {
				return nil, fmt.Errorf("gomutant: batch edit %d overlaps another edit in %s", edit.index, group.file)
			}
			out.Write(group.source[cursor:edit.start])
			out.Write(edit.new)
			cursor = edit.end
		}
		out.Write(group.source[cursor:])
		if bytes.Equal(out.Bytes(), group.source) {
			continue
		}
		source := make([]byte, out.Len())
		copy(source, out.Bytes())
		replacements = append(replacements, fileReplacement{File: group.file, Abs: group.abs, Source: source})
	}
	if len(replacements) == 0 {
		return nil, fmt.Errorf("gomutant: edit batch changes no files")
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].File < replacements[j].File })
	return replacements, ctx.Err()
}

func resolveTreeFile(root, file string) (string, error) {
	if strings.Contains(file, `\`) || path.IsAbs(file) || driveQualified(file) || path.Clean(file) != file || file == "." || strings.HasPrefix(file, "../") {
		return "", fmt.Errorf("invalid tree-relative file %q", file)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("gomutant: resolve tree root: %w", err)
	}
	abs := filepath.Join(root, filepath.FromSlash(file))
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve file %q: %w", file, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("relativize file %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q escapes the tree", file)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat file %q: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("file %q is not a regular file", file)
	}
	return resolved, nil
}

func driveQualified(file string) bool {
	return len(file) >= 2 && file[1] == ':' && ((file[0] >= 'a' && file[0] <= 'z') || (file[0] >= 'A' && file[0] <= 'Z'))
}
