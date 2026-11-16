package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func namespaceCmd(t *testing.T, value string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	addFleetSourceFlags(cmd.Flags())
	if err := cmd.Flags().Set("namespace", value); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestNamespaceFlagSingleValue(t *testing.T) {
	if got := fleetOptions(namespaceCmd(t, "payments")).K8sNamespaces; !slices.Equal(got, []string{"payments"}) {
		t.Errorf("K8sNamespaces = %q, want [payments]", got)
	}
	if got := fleetOptions(namespaceCmd(t, "")).K8sNamespaces; len(got) != 0 {
		t.Errorf("unset --namespace = %q, want every namespace", got)
	}
}

func TestNamespaceFlagList(t *testing.T) {
	if got := fleetOptions(namespaceCmd(t, " payments,ledger,,payments ")).K8sNamespaces; !slices.Equal(got, []string{"ledger", "payments"}) {
		t.Errorf("K8sNamespaces = %q, want [ledger payments]", got)
	}
}

func TestDeliveryNarrowingOnlyForOne(t *testing.T) {
	for value, want := range map[string]string{"": "", "payments": "payments", "payments,ledger": ""} {
		if got := deliveryNarrowing(flagNamespaces(namespaceCmd(t, value))); got != want {
			t.Errorf("--namespace %q narrows delivery reads to %q, want %q", value, got, want)
		}
	}
}

// The dashboard reads PACTO_DASHBOARD_NAMESPACE (or --namespace) through
// splitNamespaces and narrows delivery reads with deliveryNarrowing.
func TestDashboardDeliveryNarrowingOnlyForOne(t *testing.T) {
	for value, want := range map[string]string{"a": "a", "a,b": "", "a, a": "a"} {
		if got := deliveryNarrowing(splitNamespaces(value)); got != want {
			t.Errorf("PACTO_DASHBOARD_NAMESPACE=%q gives DeliveryNamespace %q, want %q", value, got, want)
		}
	}
}

func TestFleetStatusTextWarnsScope(t *testing.T) {
	src := fleetsrc.NewK8sSource("c1", nil, &fakeClusterClient{listJSON: `{"items":[]}`}, []string{"payments", "ledger"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := printFleetStatus(cmd, fleet.NewQuery(snap).Status(fleet.StatusQuery{}), "text"); err != nil {
		t.Fatal(err)
	}
	want := "[K8S_NAMESPACE_SCOPED] source c1 watches 2 namespaces (ledger and payments); services in any other namespace are unknown"
	if !strings.Contains(errb.String(), "warning: answer is partial") || !strings.Contains(errb.String(), want) {
		t.Errorf("stderr does not warn the scope:\n%s", errb.String())
	}
}
