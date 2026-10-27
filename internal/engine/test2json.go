// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the Go distribution's LICENSE file.

package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// test2jsonMode controls details of the test2json conversion.
type test2jsonMode int

const (
	test2jsonTimestamp test2jsonMode = 1 << iota // include Time in events
)

// testEvent is the JSON struct we emit.
type testEvent struct {
	Time        *time.Time `json:",omitempty"`
	Action      string
	Package     string     `json:",omitempty"`
	Test        string     `json:",omitempty"`
	Elapsed     *float64   `json:",omitempty"`
	Output      *textBytes `json:",omitempty"`
	OutputType  string     `json:",omitempty"`
	FailedBuild string     `json:",omitempty"`
	Key         string     `json:",omitempty"`
	Value       string     `json:",omitempty"`
	Path        string     `json:",omitempty"`
}

// textBytes is a hack to get JSON to emit a []byte as a string
// without copying it to a string.
type textBytes []byte

func (b textBytes) MarshalJSON() ([]byte, error) { return json.Marshal(string(b)) }

// A test2jsonConverter holds the state of a test-to-JSON conversion.
// It implements io.WriteCloser; the caller writes test output in,
// and the converter writes JSON output to w.
type test2jsonConverter struct {
	w           io.Writer     // JSON output
	pkg         string        // package to name in events
	mode        test2jsonMode // mode bits
	start       time.Time     // time converter started
	testName    string        // name of current test, for output attribution
	report      []*testEvent  // pending test result reports (nested for subtests)
	result      string        // overall test result if seen
	input       lineBuffer    // input buffer
	output      lineBuffer    // output buffer
	markFraming bool          // require ^V marker to introduce test framing line
	markEscape  bool          // skip the next character because it followed ^[
	markErrEnd  bool          // currently inside a ^O...^N error output block
	isFraming   bool          // currently emitting a framing output event
	failedBuild string        // package ID of failed build
}

// inBuffer and outBuffer are the input and output buffer sizes.
const (
	inBuffer  = 4096
	outBuffer = 1024
)

// newTest2JSONConverter returns a "test to json" converter.
// Writes on the returned writer are written as JSON to w,
// with minimal delay.
func newTest2JSONConverter(w io.Writer, pkg string, mode test2jsonMode) *test2jsonConverter {
	c := new(test2jsonConverter)
	*c = test2jsonConverter{
		w:     w,
		pkg:   pkg,
		mode:  mode,
		start: time.Now(),
		input: lineBuffer{
			b:    make([]byte, 0, inBuffer),
			line: c.handleInputLine,
			part: c.output.write,
		},
		output: lineBuffer{
			b:    make([]byte, 0, outBuffer),
			line: c.writeOutputEvent,
			part: c.writeOutputEvent,
		},
	}
	c.writeEvent(&testEvent{Action: "start"})
	return c
}

// Write writes the test input to the converter.
func (c *test2jsonConverter) Write(b []byte) (int, error) {
	c.input.write(b)
	return len(b), nil
}

// Exited marks the test process as having exited with the given error.
func (c *test2jsonConverter) Exited(err error) {
	if err == nil {
		if c.result != "skip" {
			c.result = "pass"
		}
	} else {
		c.result = "fail"
	}
}

const (
	markFraming  byte = 'V' &^ '@' // ^V: framing
	markErrBegin byte = 'O' &^ '@' // ^O: start of error
	markErrEnd   byte = 'N' &^ '@' // ^N: end of error
	markEscape   byte = '[' &^ '@' // ^[: escape
)

var (
	// printed by test on successful run.
	bigPass = []byte("PASS")

	// printed by test after a normal test failure.
	bigFail = []byte("FAIL")

	// printed by 'go test' along with an error if the test binary terminates
	// with an error.
	bigFailErrorPrefix = []byte("FAIL\t")

	// an === NAME line with no test name, if trailing spaces are deleted
	emptyName     = []byte("=== NAME")
	emptyNameLine = []byte("=== NAME  \n")

	updates = [][]byte{
		[]byte("=== RUN   "),
		[]byte("=== PAUSE "),
		[]byte("=== CONT  "),
		[]byte("=== NAME  "),
		[]byte("=== PASS  "),
		[]byte("=== FAIL  "),
		[]byte("=== SKIP  "),
		[]byte("=== ATTR  "),
		[]byte("=== ARTIFACTS "),
	}

	reports = [][]byte{
		[]byte("--- PASS: "),
		[]byte("--- FAIL: "),
		[]byte("--- SKIP: "),
		[]byte("--- BENCH: "),
	}

	fourSpace = []byte("    ")

	skipLinePrefix = []byte("?   \t")
	skipLineSuffix = []byte("\t[no test files]")
)

// handleInputLine handles a single whole test output line.
// It must write the line to c.output but may choose to do so
// before or after emitting other events.
func (c *test2jsonConverter) handleInputLine(line []byte) {
	if len(line) == 0 {
		return
	}
	sawMarker := false
	if c.markFraming && line[0] != markFraming {
		c.output.write(line)
		return
	}
	if line[0] == markFraming {
		c.output.flush()
		sawMarker = true
		line = line[1:]
	}

	// Trim is line without \n or \r\n.
	trim := line
	if len(trim) > 0 && trim[len(trim)-1] == '\n' {
		trim = trim[:len(trim)-1]
		if len(trim) > 0 && trim[len(trim)-1] == '\r' {
			trim = trim[:len(trim)-1]
		}
	}

	// === CONT followed by an empty test name can lose its trailing spaces.
	if bytes.Equal(trim, emptyName) {
		line = emptyNameLine
		trim = line[:len(line)-1]
	}

	// Final PASS or FAIL.
	if bytes.Equal(trim, bigPass) || bytes.Equal(trim, bigFail) || bytes.HasPrefix(trim, bigFailErrorPrefix) {
		c.flushReport(0)
		c.testName = ""
		c.markFraming = sawMarker
		c.writeFraming(line)
		if bytes.Equal(trim, bigPass) {
			c.result = "pass"
		} else {
			c.result = "fail"
		}
		return
	}

	// Special case for entirely skipped test binary: "?   \tpkgname\t[no test files]\n" is only line.
	// Report it as plain output but remember to say skip in the final summary.
	if bytes.HasPrefix(line, skipLinePrefix) && bytes.HasSuffix(trim, skipLineSuffix) && len(c.report) == 0 {
		c.result = "skip"
	}

	// "=== RUN   "
	// "=== PAUSE "
	// "=== CONT  "
	origLine := line
	ok := false
	indent := 0
	for _, magic := range updates {
		if bytes.HasPrefix(line, magic) {
			ok = true
			break
		}
	}
	if !ok {
		// "--- PASS: "
		// "--- FAIL: "
		// "--- SKIP: "
		// "--- BENCH: "
		// but possibly indented.
		for bytes.HasPrefix(line, fourSpace) {
			line = line[4:]
			indent++
		}
		for _, magic := range reports {
			if bytes.HasPrefix(line, magic) {
				ok = true
				break
			}
		}
	}

	// Not a special test output line.
	if !ok {
		if indent > 0 && indent <= len(c.report) {
			c.testName = c.report[indent-1].Test
		}
		c.output.write(origLine)
		return
	}

	// Parse out action and test name from "=== ACTION: Name".
	action, name, _ := strings.Cut(string(line[len("=== "):]), " ")
	action = strings.TrimSuffix(action, ":")
	action = strings.ToLower(action)
	name = strings.TrimSpace(name)

	e := &testEvent{Action: action}
	if line[0] == '-' { // PASS or FAIL report
		// Parse out elapsed time.
		if i := strings.Index(name, " ("); i >= 0 {
			if strings.HasSuffix(name, "s)") {
				t, err := strconv.ParseFloat(name[i+2:len(name)-2], 64)
				if err == nil {
					if c.mode&test2jsonTimestamp != 0 {
						e.Elapsed = &t
					}
				}
			}
			name = name[:i]
		}
		if len(c.report) < indent {
			// Nested deeper than expected.
			// Treat this line as plain output.
			c.output.write(origLine)
			return
		}
		// Flush reports at this indentation level or deeper.
		c.markFraming = sawMarker
		c.flushReport(indent)
		e.Test = name
		c.testName = name
		c.report = append(c.report, e)
		c.writeFraming(origLine)
		return
	}
	switch action {
	case "artifacts":
		name, e.Path, _ = strings.Cut(name, " ")
	case "attr":
		var rest string
		name, rest, _ = strings.Cut(name, " ")
		e.Key, e.Value, _ = strings.Cut(rest, " ")
	}
	// === update.
	// Finish any pending PASS/FAIL reports.
	c.markFraming = sawMarker
	c.flushReport(0)
	c.testName = name

	if action == "name" {
		// This line is only generated to get c.testName right.
		// Don't emit an event.
		return
	}

	if action == "pause" {
		c.writeFraming(origLine)
	}
	c.writeEvent(e)
	if action != "pause" {
		c.writeFraming(origLine)
	}
}

func (c *test2jsonConverter) writeFraming(line []byte) {
	c.isFraming = true
	defer func() { c.isFraming = false }()
	c.output.write(line)
}

// flushReport flushes all pending PASS/FAIL reports at levels >= depth.
func (c *test2jsonConverter) flushReport(depth int) {
	c.testName = ""
	for len(c.report) > depth {
		e := c.report[len(c.report)-1]
		c.report = c.report[:len(c.report)-1]
		c.writeEvent(e)
	}
}

// Close marks the end of the go test output.
// It flushes any pending input and then output (only partial lines at this point)
// and then emits the final overall package-level pass/fail event.
func (c *test2jsonConverter) Close() error {
	c.input.flush()
	c.output.flush()
	c.flushReport(0)
	if c.result != "" {
		e := &testEvent{Action: c.result}
		if c.mode&test2jsonTimestamp != 0 {
			dt := time.Since(c.start).Round(1 * time.Millisecond).Seconds()
			e.Elapsed = &dt
		}
		if c.result == "fail" {
			e.FailedBuild = c.failedBuild
		}
		c.writeEvent(e)
	}
	return nil
}

// writeOutputEvent writes a single output event with the given bytes.
func (c *test2jsonConverter) writeOutputEvent(out []byte) {
	var typ string
	if c.isFraming {
		typ = "frame"
	} else if c.markErrEnd {
		typ = "error-continue"
	}

	for i := 0; i < len(out); i++ {
		if c.markEscape {
			c.markEscape = false
			continue
		}

		switch out[i] {
		case markEscape:
			out = append(out[:i], out[i+1:]...)
			i--
			c.markEscape = true

		case markErrBegin:
			if i > 0 {
				out2 := out[:i]
				c.writeEvent(&testEvent{
					Action:     "output",
					Output:     (*textBytes)(&out2),
					OutputType: typ,
				})
			}
			c.markErrEnd = true
			typ = "error"
			out = out[i+1:]
			i = 0

		case markErrEnd:
			out = append(out[:i], out[i+1:]...)
			if i < len(out) && out[i] == '\n' {
				i++
			}
			out2 := out[:i]
			c.writeEvent(&testEvent{
				Action:     "output",
				Output:     (*textBytes)(&out2),
				OutputType: typ,
			})
			c.markErrEnd = false
			typ = ""
			out = out[i:]
			i = 0
		}
	}

	if len(out) > 0 {
		c.writeEvent(&testEvent{
			Action:     "output",
			Output:     (*textBytes)(&out),
			OutputType: typ,
		})
	}
}

// writeEvent writes a single event.
// It adds the package, time (if requested), and test name (if needed).
func (c *test2jsonConverter) writeEvent(e *testEvent) {
	e.Package = c.pkg
	if c.mode&test2jsonTimestamp != 0 {
		t := time.Now()
		e.Time = &t
	}
	if e.Test == "" {
		e.Test = c.testName
	}
	js, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(c.w, "testjson internal error: %v\n", err)
		return
	}
	js = append(js, '\n')
	c.w.Write(js)
}

// A lineBuffer is an I/O buffer that reacts to writes by invoking
// input-processing callbacks on whole lines or (for long lines that
// have been split) line fragments.
type lineBuffer struct {
	b       []byte       // buffer
	mid     bool         // whether we're in the middle of a long line
	line    func([]byte) // line callback
	part    func([]byte) // partial line callback
	escaped bool
}

// write writes b to the buffer.
func (l *lineBuffer) write(b []byte) {
	for len(b) > 0 {
		// Copy what we can into l.b.
		m := copy(l.b[len(l.b):cap(l.b)], b)
		l.b = l.b[:len(l.b)+m]
		b = b[m:]

		// Process lines in l.b.
		i := 0
		for i < len(l.b) {
			j, w := l.indexEOL(l.b[i:])
			if j < 0 {
				if !l.mid {
					if j := bytes.IndexByte(l.b[i:], '\t'); j >= 0 {
						if isBenchmarkName(bytes.TrimRight(l.b[i:i+j], " ")) {
							l.part(l.b[i : i+j+1])
							l.mid = true
							i += j + 1
						}
					}
				}
				break
			}
			e := i + j + w
			if l.mid {
				// Found the end of a partial line.
				l.part(l.b[i:e])
				l.mid = false
			} else {
				// Found a whole line.
				l.line(l.b[i:e])
			}
			i = e
		}

		// Whatever's left in l.b is a line fragment.
		if i == 0 && len(l.b) == cap(l.b) {
			// The whole buffer is a fragment.
			// Emit it as the beginning (or continuation) of a partial line.
			t := trimUTF8(l.b)
			l.part(l.b[:t])
			l.b = l.b[:copy(l.b, l.b[t:])]
			l.mid = true
		}

		// There's room for more input.
		// Slide it down in hope of completing the line.
		if i > 0 {
			l.b = l.b[:copy(l.b, l.b[i:])]
		}
	}
}

// indexEOL finds the index of a line ending,
// returning its position and output width.
func (l *lineBuffer) indexEOL(b []byte) (pos, wid int) {
	for i, c := range b {
		// Escape has no effect on \n
		if c == '\n' {
			return i, 1
		}

		// Ignore this character if the previous one was ^[
		if l.escaped {
			l.escaped = false
			continue
		}

		// If this character is `^[`, set the escaped flag and continue
		if c == markEscape {
			l.escaped = true
			continue
		}

		if c == markFraming && i > 0 { // test -v=json emits ^V at start of framing lines
			return i, 0
		}
	}
	return -1, 0
}

// flush flushes the line buffer.
func (l *lineBuffer) flush() {
	if len(l.b) > 0 {
		// Must be a line without a \n, so a partial line.
		l.part(l.b)
		l.b = l.b[:0]
	}
}

var benchmark = []byte("Benchmark")

// isBenchmarkName reports whether b is a valid benchmark name
// that might appear as the first field in a benchmark result line.
func isBenchmarkName(b []byte) bool {
	if !bytes.HasPrefix(b, benchmark) {
		return false
	}
	if len(b) == len(benchmark) { // just "Benchmark"
		return true
	}
	r, _ := utf8.DecodeRune(b[len(benchmark):])
	return !unicode.IsLower(r)
}

func trimUTF8(b []byte) int {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if c := b[len(b)-i]; c&0xc0 != 0x80 {
			switch {
			case c&0xe0 == 0xc0:
				if i < 2 {
					return len(b) - i
				}
			case c&0xf0 == 0xe0:
				if i < 3 {
					return len(b) - i
				}
			case c&0xf8 == 0xf0:
				if i < 4 {
					return len(b) - i
				}
			}
			break
		}
	}
	return len(b)
}
