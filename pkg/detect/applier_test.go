package detect

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestApplierNoneFoundOffersFluxUnderIDP(t *testing.T) {
	e, ok := Applier(false, false, nil, true)
	if !ok || e.Kind != fleet.ProviderApplier || e.Value != "none found" || e.Completeness != Complete || e.Source != "API discovery" ||
		e.Hint != FluxInstallHint || !strings.Contains(e.Evidence, "no applier: install Flux or Argo CD") {
		t.Errorf("applier = %+v, %v", e, ok)
	}
}

func TestApplierNoneFoundOutsideIDPHasNoHint(t *testing.T) {
	if e, ok := Applier(false, false, nil, false); !ok || e.Hint != "" || e.Value != "none found" {
		t.Errorf("applier = %+v, %v", e, ok)
	}
}

// The detector passes idp=false for a hardened profile, which renders no
// Flux: the row reads the gap and carries no proposal.
func TestApplierHardenedIDPHasNoHint(t *testing.T) {
	if e, ok := Applier(false, false, nil, IDPProfile([]string{"hardened", "idp"})); !ok || e.Hint != "" {
		t.Errorf("applier = %+v, %v", e, ok)
	}
	if !IDPProfile([]string{"idp"}) || IDPProfile(nil) || IDPProfile([]string{"default"}) {
		t.Error("IDPProfile")
	}
}

func TestApplierServedEngineGivesNoRow(t *testing.T) {
	for name, c := range map[string][2]bool{"flux": {true, false}, "argo": {false, true}} {
		if e, ok := Applier(c[0], c[1], nil, true); ok {
			t.Errorf("%s: applier = %+v", name, e)
		}
	}
}

func TestApplierDiscoveryFailedIsUnknown(t *testing.T) {
	e, ok := Applier(false, false, errors.New("timeout"), true)
	if !ok || e.Completeness != Unknown || e.Value != "" || e.Hint != "" || e.Reason != "API discovery failed: timeout" {
		t.Errorf("applier = %+v, %v", e, ok)
	}
}

func TestFluxInstallHintIsAPactoValue(t *testing.T) {
	h := FluxInstallHint
	for _, want := range []string{"--reset-then-reuse-values", "--set idp.flux.install=true", "oci://ghcr.io/trianalab/pacto/charts/pacto-operator"} {
		if !strings.Contains(h, want) {
			t.Errorf("hint misses %q: %s", want, h)
		}
	}
	if strings.Count(h, "oci://") != 1 {
		t.Errorf("hint names another chart: %s", h)
	}
	for _, bad := range []string{"helm install", "flux install", "flux bootstrap", "kubectl apply"} {
		if strings.Contains(h, bad) {
			t.Errorf("hint holds %q: %s", bad, h)
		}
	}
}

func TestApplierHintIsTheCompiledConstant(t *testing.T) {
	res := Result{AsOf: asOf, Cluster: "uid", Entries: []Entry{
		{Kind: "applier", Value: "none found", Completeness: Complete, Hint: "helm install x oci://evil/x"},
		{Kind: "registry", Completeness: Complete, Hint: "--set registry=ghcr.io/acme"},
	}}
	rows := ProviderRows(res, "detection", asOf)
	if r := row(t, rows, "applier"); r.Hint != FluxInstallHint || r.Name != "Applier" ||
		r.UsedFor[0] != "proposal for delivery in profile idp; set it on the Pacto release yourself" {
		t.Errorf("applier row = %+v", r)
	}
	if r := row(t, rows, "registry"); r.Hint != "--set registry=ghcr.io/acme" {
		t.Errorf("registry row = %+v", r)
	}
	res.Entries = res.Entries[:1]
	res.Entries[0].Hint = ""
	if r := row(t, ProviderRows(res, "detection", asOf), "applier"); r.Hint != "" {
		t.Errorf("a row with no hint got one: %+v", r)
	}
}

func TestApplierRowCarriesNoNamespace(t *testing.T) {
	e, _ := Applier(false, false, nil, true)
	r := row(t, ProviderRows(Result{AsOf: asOf, Cluster: "uid", Entries: []Entry{e}}, "detection", asOf), "applier")
	if r.Objects != nil || r.Scope != "" || r.ClusterID != "uid" || r.Provenance != fleet.ProvenanceInferred {
		t.Errorf("applier row = %+v", r)
	}
}
