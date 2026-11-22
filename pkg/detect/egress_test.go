package detect

import (
	"errors"
	"fmt"
	"reflect"
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
	if !slices.Equal(EgressKinds, []string{"istio-egress", "cilium-fqdn", "egress-gateway"}) {
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

func gatewayPods(n int) []Object {
	out := make([]Object, n)
	for i := range out {
		out[i] = Object{Kind: "Pod", Namespace: "istio-system", Name: fmt.Sprintf("istio-egressgateway-%d", i)}
	}
	return out
}

func TestEgressGatewayEntryAutoHasHint(t *testing.T) {
	e := EgressGateways(gatewayPods(1), GatewayLogsAuto, nil)
	if e.Kind != "egress-gateway" || e.Completeness != Partial || e.Reason != "needs pods/log" || e.Scope != fleet.ScopeCluster ||
		e.Hint != "helm upgrade <release> <chart> --reuse-values --set controller.egressGatewayLogs=on" ||
		e.Evidence != "1 Istio egress gateway found; logs: auto" {
		t.Errorf("auto = %+v", e)
	}
	// An unrecognised or empty mode is auto: it reads nothing.
	if e := EgressGateways(gatewayPods(2), "", nil); e.Completeness != Partial || e.Evidence != "2 Istio egress gateways found; logs: " {
		t.Errorf("empty mode = %+v", e)
	}
}

func TestEgressGatewayEntryOnComplete(t *testing.T) {
	if e := EgressGateways(gatewayPods(2), GatewayLogsOn, nil); e.Completeness != Complete || e.Reason != "" || e.Hint != "" || len(e.Objects) != 2 {
		t.Errorf("on = %+v", e)
	}
}

func TestEgressGatewayEntryOffUnknown(t *testing.T) {
	if e := EgressGateways(gatewayPods(1), GatewayLogsOff, nil); e.Completeness != Unknown || e.Reason != "turned off by controller.egressGatewayLogs" || e.Hint != "" {
		t.Errorf("off = %+v", e)
	}
	if e := EgressGateways(nil, GatewayLogsOn, errors.New("Forbidden")); e.Kind != "egress-gateway" || e.Completeness != Unknown ||
		!strings.Contains(e.Reason, "Forbidden") || e.Scope != fleet.ScopeCluster {
		t.Errorf("error = %+v", e)
	}
}

func TestNoGatewayNoEntry(t *testing.T) {
	for _, mode := range []string{GatewayLogsAuto, GatewayLogsOn, GatewayLogsOff} {
		if e := EgressGateways(nil, mode, nil); e.Kind != "" {
			t.Errorf("%s: entry = %+v", mode, e)
		}
	}
}

func TestEgressGatewayEntryEvidenceNamesNoNamespace(t *testing.T) {
	e := EgressGateways(gatewayPods(MaxNamed+3), GatewayLogsAuto, nil)
	if strings.Contains(e.Evidence, "istio-system") || strings.Contains(e.Source, "istio-system") || len(e.Objects) != MaxNamed {
		t.Errorf("entry = %+v", e)
	}
	row := ProviderRows(Result{AsOf: asOf, Entries: []Entry{EgressGateways(gatewayPods(1), GatewayLogsAuto, nil)}}, "detected", asOf)[0]
	if strings.Contains(row.Evidence, "istio-system") || len(row.Objects) != 1 || row.Objects[0].Namespace != "istio-system" ||
		!slices.Contains(row.UsedFor, fleet.UseExternalEgress) {
		t.Errorf("row = %+v", row)
	}
}

// TestEgressDetectionRowsRedactedForNamespaceReader: the three egress
// detection rows come from cluster-wide reads, so a reader of one namespace
// sees each redacted: no hint, no count, no gateway pod.
func TestEgressDetectionRowsRedactedForNamespaceReader(t *testing.T) {
	entries := []Entry{IstioEgress(true, nil), CiliumFQDN(true, nil), EgressGateways(gatewayPods(1), GatewayLogsAuto, nil)}
	rows := ProviderRows(Result{AsOf: asOf, Entries: entries}, "detection", asOf)
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: func() time.Time { return asOf }},
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: rows}))
	if err != nil {
		t.Fatal(err)
	}
	providers := fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "shop" }, nil).SnapshotForSerialization().Providers
	for _, kind := range []string{"istio-egress", "cilium-fqdn", "egress-gateway"} {
		got := row(t, providers, kind)
		if got.Hint != "" || len(got.Objects) != 0 || strings.ContainsAny(got.Evidence, "0123456789") || strings.Contains(got.Evidence, "CRD") {
			t.Errorf("%s: %+v", kind, got)
		}
	}
}

// TestDetectEntriesFitMaxEntries: the operator's detector emits at most one
// entry per kind it names, so every kind together stays within MaxEntries,
// past which Encode refuses the whole result.
func TestDetectEntriesFitMaxEntries(t *testing.T) {
	if len(rowNames) > MaxEntries {
		t.Fatalf("%d detectable kinds, more than MaxEntries %d: %v", len(rowNames), MaxEntries, rowNames)
	}
	for _, kind := range EgressKinds {
		if _, ok := rowNames[kind]; !ok {
			t.Errorf("egress kind %s has no row name", kind)
		}
	}
}

// TestReaderOffLowersOnlyACompleteEntryWithNoReader: an entry whose reader does not run never reads
// complete; a running reader or an entry already short of complete is unchanged.
func TestReaderOffLowersOnlyACompleteEntryWithNoReader(t *testing.T) {
	served := IstioEgress(true, nil)
	if got := ReaderOff(served, false, "why", "hint"); got.Completeness != Partial || got.Reason != "why" || got.Hint != "hint" {
		t.Errorf("reader off = %+v", got)
	}
	if got := ReaderOff(served, true, "why", "hint"); !reflect.DeepEqual(got, served) {
		t.Errorf("running reader = %+v", got)
	}
	if missing := IstioEgress(false, nil); !reflect.DeepEqual(ReaderOff(missing, false, "why", "hint"), missing) {
		t.Errorf("an unknown entry changed")
	}
}
