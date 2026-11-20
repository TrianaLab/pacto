package detect

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var hinted = Entry{Kind: "registry", Value: "ghcr.io/acme", Hint: "oci://ghcr.io/acme/<service>:<version>"}

func pulling(id, ns, image string, secrets ...string) Workload {
	return Workload{ID: id, Namespace: ns, Images: []string{image}, PullSecrets: secrets}
}

func TestPullSecretPicksMostUsedOnTopHost(t *testing.T) {
	ws := []Workload{
		pulling("a", "shop", "ghcr.io/acme/web:1", "ghcr-pull", "ghcr-pull"),
		pulling("b", "shop", "ghcr.io/other/api:1", "ghcr-pull"),
		pulling("c", "pay", "ghcr.io/acme/pay:1", "pay-pull"),
		pulling("d", "q", "quay.io/x/y:1", "quay-pull"),
		pulling("e", "q", "quay.io/x/z:1", "quay-pull"),
		pulling("f", "q", "quay.io/x/w:1", "quay-pull"),
	}
	e := PullSecret(ws, hinted, true)
	if e.Value != "shop/ghcr-pull" || !strings.Contains(e.Evidence, "of 2 running workloads that pulled from ghcr.io") || e.Scope != "" {
		t.Errorf("entry = %+v", e)
	}
	one := PullSecret(ws[2:3], hinted, true)
	if !strings.Contains(one.Evidence, "of 1 running workload that") {
		t.Errorf("evidence = %q", one.Evidence)
	}
}

func TestPullSecretTieBreaksByName(t *testing.T) {
	ws := []Workload{pulling("a", "shop", "ghcr.io/acme/web:1", "zz"), pulling("b", "shop", "ghcr.io/acme/api:1", "aa")}
	if e := PullSecret(ws, hinted, true); e.Value != "shop/aa" {
		t.Errorf("entry = %+v", e)
	}
}

func TestPullSecretHintOnlyWithRegistryHint(t *testing.T) {
	ws := []Workload{pulling("a", "shop", "ghcr.io/acme/web:1", "ghcr-pull")}
	if e := PullSecret(ws, hinted, true); e.Hint != "--set registryPullSecret=shop/ghcr-pull" {
		t.Errorf("hint = %q", e.Hint)
	}
	if e := PullSecret(ws, Entry{Kind: "registry", Value: "ghcr.io/acme"}, true); e.Hint != "" {
		t.Errorf("no clear registry majority, still a hint: %q", e.Hint)
	}
}

func TestPullSecretIsInferredAndProposed(t *testing.T) {
	e := PullSecret([]Workload{pulling("a", "shop", "ghcr.io/acme/web:1", "ghcr-pull")}, hinted, false)
	if e.Kind != "pull-secret" || e.Value != "shop/ghcr-pull" || len(e.Namespaces) != 1 || e.Namespaces[0] != "shop" || e.Completeness != Partial ||
		!strings.Contains(e.Evidence, "named in the pod specs of") || !strings.Contains(e.Evidence, "Pacto did not read the Secret") {
		t.Fatalf("entry = %+v", e)
	}
	p := row(t, ProviderRows(Result{AsOf: asOf, Entries: []Entry{e}}, "detection", asOf), "pull-secret")
	if p.Provenance != fleet.ProvenanceInferred || p.Name != "Pull Secret" || len(p.Objects) != 1 ||
		!strings.Contains(p.UsedFor[0], "a pod pull Secret usually grants pull only") || !strings.Contains(p.UsedFor[0], "registryPullSecret") {
		t.Errorf("row = %+v", p)
	}
}

func TestPullSecretNoneWhenNoPodNamesOne(t *testing.T) {
	ws := []Workload{pulling("a", "shop", "ghcr.io/acme/web:1"), pulling("b", "shop", "quay.io/x/y:1", "quay-pull"), pulling("c", "shop", "ghcr.io/acme/x:1", "")}
	if e := PullSecret(ws, hinted, true); e.Kind != "" {
		t.Errorf("entry = %+v", e)
	}
	if e := PullSecret(ws, Entry{Kind: "registry"}, true); e.Kind != "" {
		t.Errorf("no registry value, still an entry: %+v", e)
	}
}
