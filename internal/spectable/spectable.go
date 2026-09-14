// Package spectable is test support: it reads one requirement's section
// out of a spec document and splits a markdown table's columns, so the
// faces' surface pins parse the surface table one way.
package spectable

import (
	"os"
	"strings"
	"testing"
)

// Section returns the requirement's section of the spec document — from
// its bold id to the next requirement's — with the document read
// relative to the calling package's directory.
func Section(t *testing.T, relSpecPath, requirement string) string {
	t.Helper()
	spec, err := os.ReadFile(relSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	section := string(spec)
	start := strings.Index(section, "**"+requirement+"**")
	if start < 0 {
		t.Fatalf("%s not found in %s", requirement, relSpecPath)
	}
	section = section[start:]
	if end := strings.Index(section[1:], "**REQ-"); end >= 0 {
		section = section[:end+1]
	}
	return section
}

// Columns is a markdown table's cells in the given 1-based columns, row
// by row, joined by newlines — the header row and the separator under
// it skipped — so a number stated in another column is never read.
func Columns(t *testing.T, table string, columns ...int) string {
	t.Helper()
	var cells []string
	rows := 0
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		if rows++; rows <= 2 {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		for _, c := range columns {
			if c-1 < len(fields) {
				cells = append(cells, fields[c-1])
			}
		}
	}
	if len(cells) == 0 {
		t.Fatal("the table has no rows")
	}
	return strings.Join(cells, "\n")
}
