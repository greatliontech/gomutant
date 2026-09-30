// Package integrationtest is the repository's integration build
// selection. Every test that loads a fixture tree, spawns go test, or
// runs a measured campaign is -short-gated (gofresh's shortgates pins
// the gates' placement; the pairing pin here pins that every
// gate-carrying package's TestMain takes the default); under the
// default build selection such a suite's TestMain runs as -short unless
// the command line says otherwise, so the package's default test binary
// is its unit suite — seconds, and the oracle gomutant derives for the
// package's own symbols when it measures itself — while `-tags
// integration` leaves the flag to the command line and every test runs.
// A symbol only an integration test reaches reads never-executed under
// the default selection, truthfully: that symbol's oracle is the
// integration selection's campaign.
package integrationtest

import "flag"

// DefaultToShort is called by a suite's TestMain before m.Run: outside
// the integration selection it sets -test.short as the flag's default,
// which an explicit -test.short=false on the command line still
// overrides (m.Run parses the command line after it). Inside the
// selection it does nothing.
func DefaultToShort() {
	if Enabled {
		return
	}
	if err := flag.Set("test.short", "true"); err != nil {
		panic("integrationtest: " + err.Error())
	}
}
