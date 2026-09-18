package mcpserver

import (
	"io"
	"os"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

// serverSeams are the package's test injection points — the observers
// a test installs and the paces a test tightens — in one struct, read
// through the one variable seams; the one seam outside it,
// Server.updateDocument, is per server rather than per package and
// travels with the instance a test builds. Production never writes a field:
// each stays at its default (a nil observer, the production pace,
// stderr), so a seam is never a behaviour switch. The package's tests
// never run in parallel; a test writes a field and restores it.
type serverSeams struct {
	// afterCommit and afterFinalReplacement observe a run's two commit
	// boundaries — after an incremental commit returned, and after the
	// final replacement returned — so a test can end the request
	// exactly there and pin what each boundary claims
	// (REQ-exec-banked-summary, REQ-exec-cancellation).
	afterCommit           func(gomutant.Finding)
	afterFinalReplacement func()
	// stretchObserver sees every stretch label a run records and
	// selectionObserver the dispatch's start (the inputs were read at
	// preparation), so a test can pin the labels' order against the
	// work.
	stretchObserver   func(string)
	selectionObserver func()
	// heartbeatInterval paces withHeartbeat's still-working
	// notifications on the one cadence every face's progress keeps; a
	// seam so the emission is testable without a thirty-second test.
	heartbeatInterval time.Duration
	// exitLogNotice receives the one line the server writes outside its
	// log — that the log is unwritable — so serving never fails on its
	// own diagnostics; production is stderr.
	exitLogNotice io.Writer
}

// seams is the one variable tests write; defaultSeams is what
// production reads.
var seams = defaultSeams()

func defaultSeams() serverSeams {
	return serverSeams{
		heartbeatInterval: gomutant.ProgressCadence,
		exitLogNotice:     os.Stderr,
	}
}
