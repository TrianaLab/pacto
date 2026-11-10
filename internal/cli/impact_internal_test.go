package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// The text output names each guarantee a consumer requires that the new
// revision refuses, as the JSON `unmet` field and the TUI do.
func TestPrintImpactResult_Unmet(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	m := contract.Mismatch{Consumer: "web", Provider: "api", Interface: "events", Guarantee: "delivery", Required: "at-least-once"}
	res := &impact.Result{Service: "api", Classification: "BREAKING", Consumers: []impact.AffectedConsumer{
		{Service: "web", Direct: true, Unmet: []contract.Mismatch{m}},
	}}
	if err := printImpactResult(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	if want := "    refused: " + m.String() + "\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
}

// runImpactInternal runs `pacto impact` built with d in format, returning
// stdout and the error.
func runImpactInternal(t *testing.T, d deps, format string, args ...string) (string, error) {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, format)
	cmd := newImpactCommand(newTestService(t), v, d)
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// regressionFleet is a fleet root with payments, so a regressed event has a
// subject.
func regressionFleet(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  name: payments\n  version: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRegressionUnreadIsLimitation: with no cluster to read delivery from,
// the answer carries the limitation and never says no failure was recorded.
func TestRegressionUnreadIsLimitation(t *testing.T) {
	d := defaultDeps()
	d.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	out, err := runImpactInternal(t, d, "json", "--regressed", "payments@"+cliA, "--local", regressionFleet(t))
	if err != nil {
		t.Fatal(err) // nothing depends on payments and the snapshot is complete
	}
	var res impact.Result
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatal(jerr)
	}
	found := false
	for _, l := range res.Limitations {
		found = found || (l.Code == impact.LimitationDeliveryEvidenceUnavailable && strings.Contains(l.Message, "no cluster to read delivery from: no kubeconfig"))
	}
	if !found || strings.Contains(res.GateReason, "recorded no failure") {
		t.Fatalf("an unread delivery is a limitation: %+v", res)
	}
}

// TestRegressionReadsCallerCluster: a reachable cluster is read with the
// caller's credentials; nothing recorded is said as such.
func TestRegressionReadsCallerCluster(t *testing.T) {
	out, err := runImpactInternal(t, deliveryDeps("alice", nil), "text", "--regressed", "payments@"+cliA, "--local", regressionFleet(t))
	if err != nil || !strings.Contains(out, "Event: payments regressed at "+cliA) {
		t.Fatalf("regressed = %v\n%s", err, out)
	}
	if _, err := runImpactInternal(t, defaultDeps(), "text", "--regressed", "payments", "--local", regressionFleet(t)); err == nil {
		t.Fatal("a regressed event names its revision")
	}
}

// TestPrintImpactEventText: lanes, the why line, bounded evidence,
// resources and the delivery evidence block.
func TestPrintImpactEventText(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	asOf := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	seen := asOf.Add(-90 * time.Minute)
	ev := []impact.Evidence{{Kind: "target", Source: "prod/web", At: &seen}, {Kind: "observed-call", Source: "mira"},
		{Kind: "target", Source: "old/web", Stale: true}, {Kind: "target", Source: "x"}}
	res := &impact.Result{Service: "redis", AsOf: asOf, Completeness: "complete", Gate: impact.GateUnverified,
		Event: &impact.Event{Kind: impact.EventRegressed, Service: "redis", Revision: "sha256:a"},
		Consumers: []impact.AffectedConsumer{
			{Service: "web", Direct: true, Effect: impact.EffectBreaks, Path: []string{"web", "redis"}, Provenance: "declared", Via: []string{"dependency"}, Evidence: ev,
				Resources: []fleet.ProvisioningStatus{{Environment: "prod", Phase: "Ready"}}, ResourcesTotal: 3},
			{Service: "price", Effect: impact.EffectMayDegrade, Path: []string{"price", "redis"}},
		},
		Regression: []impact.RegressionEvidence{{Namespace: "prod", Stage: "redis-prod", Kind: impact.RegressionRollback, Message: "health failed"},
			{Namespace: "prod", Stage: "redis-prod", Kind: impact.RegressionCheck, Gate: "health", Message: "5xx", Record: "r1"}},
		Limitations: []fleet.Limitation{{Code: impact.LimitationDeliveryEvidenceUnavailable, Message: "namespace test not read"}},
	}
	if err := printImpactResult(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Event: redis regressed at sha256:a  asOf 2026-11-10T12:00:00Z  completeness complete",
		"BREAKS (1):", "MAY DEGRADE (1):", "effect=breaks",
		"    path: web > redis  provenance: declared  via: dependency",
		"    evidence: target prod/web 1h30m0s before asOf", "    evidence: observed-call mira no observation window",
		"    evidence: target old/web stale", "    and 1 more", "    resources: prod Ready", "    and 2 more resources",
		"    path: price > redis  provenance: -  via: -",
		"Delivery evidence (2):", "prod/redis-prod rollback health failed (from status, not signed)", "prod/redis-prod check health 5xx\n",
		"Delivery evidence not read: namespace test not read",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "UNKNOWN (") {
		t.Errorf("an empty lane is not printed:\n%s", buf.String())
	}
}
