package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// runFleetEntity runs `fleet entity` over the demo bundles with d.
func runFleetEntity(t *testing.T, d deps, args ...string) string {
	t.Helper()
	t.Setenv(trustEnv, "")
	v := viper.New()
	v.Set(outputFormatKey, "text")
	cmd := newFleetCommand(newTestService(t), v, d)
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(append([]string{"entity", "service", "payments-service", "--local", "../../examples/demo/bundles"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("entity: %v", err)
	}
	return out.String()
}

func TestFleetEntityReachedNoClusterNotRead(t *testing.T) {
	out := runFleetEntity(t, deliveryDeps("alice", errors.New("no kubeconfig")), "--reached")
	if !strings.Contains(out, "times not-read: no cluster to read delivery from: no kubeconfig") || !strings.Contains(out, "time not read\n") {
		t.Fatalf("out:\n%s", out)
	}
	// A --local fleet keys revisions by their files: no record can name one,
	// so its times are not read, never "not recorded".
	out = runFleetEntity(t, deliveryDeps("alice", nil), "--reached")
	if !strings.Contains(out, "times not-read: a local revision is keyed by its files") || strings.Contains(out, "time not recorded") {
		t.Fatalf("out:\n%s", out)
	}
	// Without --reached the cluster is never asked.
	if out := runFleetEntity(t, deps{}); !strings.Contains(out, "times not-read: ask with reached") {
		t.Fatalf("out:\n%s", out)
	}
}

// TestMCPReachedStdioOnly: only a server over stdio, whose caller is this
// process's own user, reads delivery for reached.
func TestMCPReachedStdioOnly(t *testing.T) {
	if stdioReached(nil, deps{}, "http") != nil || stdioReached(nil, deps{}, "stdio") == nil {
		t.Fatal("reached must be read over stdio only")
	}
}
