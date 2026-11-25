package cli

import (
	"os"
	"testing"
)

// TestMain points KUBECONFIG at a file that does not exist, so no test reads
// the developer's cluster through the default deps (clusterConfig, served);
// a test that wants a cluster sets its own.
func TestMain(m *testing.M) {
	_ = os.Setenv("KUBECONFIG", os.DevNull+".pacto-test-missing")
	os.Exit(m.Run())
}
