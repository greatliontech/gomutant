package gomutant

import (
	"testing"

	"github.com/greatliontech/gofresh/resident"
)

// The reading both faces carry is the fleet's words at the moment
// "now" through the face's sampler, and its tail form leads with the
// dash; a host answering no reading yields nothing on either
// (REQ-exec-run-status).
func TestResidentReadingIsTheFleetsWordsOrNothing(t *testing.T) {
	set := resident.Set{ProcessBytes: 10 << 20, ProcessPeakBytes: 20 << 20, CeilingBytes: 4 << 30}
	want := resident.Words(set, "now")
	if got := ResidentReading(func() (resident.Set, bool) { return set, true }); got != want {
		t.Fatalf("reading = %q, want %q", got, want)
	}
	if got := ResidentTail(want); got != " — "+want {
		t.Fatalf("tail = %q, want the dash-led reading", got)
	}
	if got := ResidentReading(func() (resident.Set, bool) { return set, false }); got != "" {
		t.Fatalf("no reading answered %q, want nothing", got)
	}
	if got := ResidentTail(""); got != "" {
		t.Fatalf("the tail of no reading = %q, want nothing", got)
	}
}
