package app

import (
	"os"
	"testing"
)

// Tests never read the developer's real private-key directory. Discovery tests
// opt into an isolated native directory explicitly.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv("TAILCHASE_TAILCAT_KEYS_DIR"); !set {
		_ = os.Setenv("TAILCHASE_TAILCAT_KEYS_DIR", "")
	}
	os.Exit(m.Run())
}
