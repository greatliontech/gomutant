//go:build !unix

package gomutant

// AcquireCampaignLock on non-unix platforms is a documented no-op:
// campaigns run without the advisory exclusivity of
// REQ-exec-exclusivity there. Linux is the supported platform; the
// no-op keeps the package compiling on the others without pretending a
// lock discipline it does not have.
func AcquireCampaignLock(path string) (release func(), err error) {
	return func() {}, nil
}
