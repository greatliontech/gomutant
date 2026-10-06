package gomutant

import "github.com/greatliontech/gofresh/resident"

// ResidentReading is the process's resident reading in the fleet's
// words (gofresh/resident), the moment "now" — the tail of both faces'
// progress lines (REQ-exec-run-status) — taken through sample, the
// face's seam over resident.Sample; "" where the host answers no
// reading.
func ResidentReading(sample func() (resident.Set, bool)) string {
	set, ok := sample()
	if !ok {
		return ""
	}
	return resident.Words(set, "now")
}

// ResidentTail is a reading as a line's tail, nothing for none.
func ResidentTail(reading string) string {
	if reading == "" {
		return ""
	}
	return " — " + reading
}
