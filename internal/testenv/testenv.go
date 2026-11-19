// Package testenv reads the environment variables that turn on the tests
// that need a real container API server. The tests skip unless the variables
// are set, so `go test ./...` runs without a server.
package testenv

import (
	"os"
	"strconv"
	"testing"
)

// Live turns on the tests that talk to a running API server. They use
// only read-only routes.
const Live = "GO_CONTAINER_TEST_LIVE"

// Enabled reports whether the variable name is set to a true value, such
// as 1 or true.
func Enabled(name string) bool {
	v, err := strconv.ParseBool(os.Getenv(name))
	return err == nil && v
}

// RequireLive skips the test unless Live is set.
func RequireLive(t testing.TB) {
	t.Helper()
	require(t, Live, "tests against the container API server")
}

func require(t testing.TB, name, what string) {
	t.Helper()
	if !Enabled(name) {
		t.Skipf("set %s=1 to run %s", name, what)
	}
}
