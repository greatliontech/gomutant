//go:build integration

package integrationtest

// Enabled reports the integration build selection: true under
// `-tags integration`, where the command line alone decides -short and
// every test runs.
const Enabled = true
