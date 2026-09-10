package gomutant

// mergeOnly is the tests' view of a merge that also reports the
// attestations it shed: the merged findings alone.
func mergeOnly(findings []Finding, _ []AttestationShed) []Finding { return findings }
