package detect

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestIstioEgressEntry(t *testing.T) {
	if e := IstioEgress(true, nil); e.Kind != "istio-egress" || e.Evidence != "ServiceEntry CRD present" || e.Completeness != Complete || e.Scope != fleet.ScopeCluster {
		t.Errorf("served = %+v", e)
	}
	if e := IstioEgress(false, nil); e.Evidence != "no ServiceEntry CRD" || e.Completeness != Unknown || e.Reason == "" || e.Scope != fleet.ScopeCluster {
		t.Errorf("not served = %+v", e)
	}
}

func TestIstioEgressEntryErrorIsUnknown(t *testing.T) {
	e := IstioEgress(true, errors.New("forbidden"))
	if e.Completeness != Unknown || !strings.Contains(e.Reason, "forbidden") || e.Evidence != "" || e.Scope != fleet.ScopeCluster {
		t.Errorf("entry = %+v", e)
	}
}

func TestEgressRowsUseExternalEgress(t *testing.T) {
	if !slices.Equal(EgressKinds, []string{"istio-egress", "cilium-fqdn"}) {
		t.Fatalf("EgressKinds = %v", EgressKinds)
	}
	asOf := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{IstioEgress(true, nil), Metrics(true, nil)}}, "detected", asOf)
	if p := rows[0]; p.Name != "Istio egress" || p.Scope != fleet.ScopeCluster || p.Status != fleet.SourceAvailable ||
		!slices.Equal(p.UsedFor, []string{"reads calls leaving the cluster; nothing is turned on", fleet.UseExternalEgress}) {
		t.Errorf("istio-egress row = %+v", p)
	}
	if p := rows[1]; slices.Contains(p.UsedFor, fleet.UseExternalEgress) {
		t.Errorf("metrics row = %+v", p)
	}
	if p := ProviderRows(Result{AsOf: asOf, Entries: []Entry{IstioEgress(false, nil)}}, "detected", asOf)[0]; p.Status != fleet.SourceUnavailable ||
		len(p.Limitations) != 1 || p.Limitations[0].Code != "DETECTION_ISTIO_EGRESS_UNKNOWN" {
		t.Errorf("not served row = %+v", p)
	}
}

func TestCiliumFQDNEntry(t *testing.T) {
	if e := CiliumFQDN(true, nil); e.Kind != "cilium-fqdn" || e.Evidence != "CiliumNetworkPolicy CRD present" || e.Completeness != Complete || e.Scope != fleet.ScopeCluster {
		t.Errorf("served = %+v", e)
	}
	if e := CiliumFQDN(false, nil); e.Evidence != "no CiliumNetworkPolicy CRD" || e.Completeness != Unknown || !strings.Contains(e.Reason, "cilium.io/v2") || e.Scope != fleet.ScopeCluster {
		t.Errorf("not served = %+v", e)
	}
	if e := CiliumFQDN(true, errors.New("forbidden")); e.Completeness != Unknown || !strings.Contains(e.Reason, "forbidden") || e.Scope != fleet.ScopeCluster {
		t.Errorf("error = %+v", e)
	}
	asOf := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	if p := ProviderRows(Result{AsOf: asOf, Entries: []Entry{CiliumFQDN(true, nil)}}, "detected", asOf)[0]; p.Name != "Cilium FQDN" ||
		!slices.Equal(p.UsedFor, []string{"reads calls leaving the cluster; nothing is turned on", fleet.UseExternalEgress}) {
		t.Errorf("row = %+v", p)
	}
}
