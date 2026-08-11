// Package gopter is a hermetic stub of github.com/leanovate/gopter: the
// property-runtime detection resolves the import path, so the fixture
// needs the shapes, not the behavior. gopter registers no invocation
// flags - which is exactly why gomutant cannot pin its draws.
package gopter

// Properties mirrors the suite handle.
type Properties struct{}

// NewProperties mirrors the constructor.
func NewProperties() *Properties { return &Properties{} }
