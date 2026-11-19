// Package testenv reads the environment variables that turn on the tests
// that need a real container API server. The tests skip unless the variables
// are set, so `go test ./...` runs without a server.
package testenv

import (
	"os"
	"strconv"
	"testing"
)

const (
	// Live turns on the tests that talk to a running API server. They use
	// only read-only routes.
	Live = "GO_CONTAINER_TEST_LIVE"

	// Write also turns on the live tests that change the state of the
	// server, such as tests that create and delete images, volumes, and
	// networks. Each test removes what it creates. Write has no effect
	// without Live.
	Write = "GO_CONTAINER_TEST_WRITE"

	// VM turns on the tests that boot a container VM. These need an Apple
	// silicon Mac that can start VMs. GitHub's hosted macOS runners cannot.
	VM = "GO_CONTAINER_TEST_VM"
)

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

// RequireWrite skips the test unless both Live and Write are set.
func RequireWrite(t testing.TB) {
	t.Helper()
	RequireLive(t)
	require(t, Write, "tests that change the state of the API server")
}

// RequireVM skips the test unless VM is set. It does not check Live or
// Write. A live test that boots a VM also calls RequireWrite.
func RequireVM(t testing.TB) {
	t.Helper()
	require(t, VM, "tests that boot a container VM")
}

func require(t testing.TB, name, what string) {
	t.Helper()
	if !Enabled(name) {
		t.Skipf("set %s=1 to run %s", name, what)
	}
}
