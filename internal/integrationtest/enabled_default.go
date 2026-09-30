//go:build !integration

package integrationtest

// Enabled reports the integration build selection: false under the
// default selection, where a suite's TestMain runs as -short by
// default and the test binary is its unit suite.
const Enabled = false
