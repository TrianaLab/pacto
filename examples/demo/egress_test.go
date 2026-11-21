package main

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoEgressRows: the demo shows the four egress readers. The cluster
// source reports the kernel-egress row, partial with the node agent on 3 of 4
// nodes and naming carrier-gateway; the operator detected the Istio egress
// API, no Cilium policy API and one egress gateway whose logs it may not read.
// Every row reads calls leaving the cluster and is cluster-scoped.
func TestDemoEgressRows(t *testing.T) {
	snap := demoFleet(t)
	want := map[string]struct {
		source string
		status fleet.SourceStatus
	}{
		fleet.ProviderKernelEgress: {"k8s-production", fleet.SourcePartial},
		"istio-egress":             {"cluster-detection", fleet.SourceAvailable},
		"cilium-fqdn":              {"cluster-detection", fleet.SourceUnavailable},
		"egress-gateway":           {"cluster-detection", fleet.SourcePartial},
	}
	got := map[string]fleet.Provider{}
	for _, p := range snap.Providers {
		if slices.Contains(p.UsedFor, fleet.UseExternalEgress) {
			got[p.ID] = p
		}
	}
	if len(got) != len(want) {
		t.Fatalf("egress rows = %v, want %v", slices.Sorted(maps.Keys(got)), want)
	}
	for id, w := range want {
		p, ok := got[id]
		if !ok || p.Source != w.source || p.Status != w.status || p.Scope != fleet.ScopeCluster {
			t.Errorf("row %s = %+v, want source %s, status %s, cluster scope", id, p, w.source, w.status)
		}
	}
	checkEgressRowDetails(t, got[fleet.ProviderKernelEgress], got["egress-gateway"])
}

// checkEgressRowDetails: the kernel-egress row says how many nodes run the
// agent, why it is partial and whose calls it read; the gateway row counts
// its gateway, names the switch and keeps the pod as an object.
func checkEgressRowDetails(t *testing.T, kernel, gw fleet.Provider) {
	t.Helper()
	if kernel.Evidence != "node agent on 3/4 nodes" || len(kernel.Limitations) != 1 ||
		!strings.Contains(kernel.Limitations[0].Message, "node agent") ||
		!slices.Equal(kernel.Services, []fleet.ServiceKey{fleet.NewServiceKey("carrier-gateway")}) {
		t.Errorf("kernel-egress row = %+v", kernel)
	}
	if !strings.Contains(gw.Evidence, "1 Istio egress gateway found; logs: auto") || !strings.Contains(gw.Hint, "controller.egressGatewayLogs=on") ||
		!slices.ContainsFunc(gw.Objects, func(o fleet.ProviderObject) bool { return o.Namespace == "istio-system" }) {
		t.Errorf("egress-gateway row = %+v", gw)
	}
}

// TestDemoKernelEgressExternal: carrier-gateway calls an SFTP host of a
// partner that no contract, trace or catalog names. Only the node agent saw
// it, so the External groups the host by its registrable domain, stays
// unclassified (the edge carries the host, never the operator's class), reads
// observed with no declared source, and its one observed source is the
// cluster source, never the trace source.
func TestDemoKernelEgressExternal(t *testing.T) {
	snap := demoFleet(t)
	rec := snap.Externals["host/northwind-partners.com"]
	if rec == nil || rec.Class != externalcatalog.ClassUnclassified {
		t.Fatalf("host/northwind-partners.com external = %+v, want an unclassified one", rec)
	}
	for _, s := range rec.Sources {
		if s.Kind != fleet.ExternalSourceObserved || s.Provenance != fleet.ProvenanceObserved {
			t.Errorf("source %+v, want only observed", s)
		}
	}
	view, err := fleet.NewQuery(snap).GetExternal("host/northwind-partners.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ObservedSources) != 1 || view.ObservedSources[0].Source != "k8s-production" ||
		view.ObservedSources[0].Service != fleet.NewServiceKey("carrier-gateway") {
		t.Errorf("observed sources = %+v, want one from k8s-production", view.ObservedSources)
	}
}

// TestDemoKernelEgressRowMatchesFleetsrc pins the demo's kernel-egress row
// equal to the row internal/fleetsrc builds from a Pacto whose egress
// coverage is partial because one of four nodes has no node agent, so the
// wording cannot drift.
func TestDemoKernelEgressRowMatchesFleetsrc(t *testing.T) {
	var view delivery.PactoView
	status := `{"status": {"statusFormat": 4, "lastReconciledAt": "` + demoNow().Format(time.RFC3339) + `",
		"contract": {"serviceName": "carrier-gateway"},
		"observedEgressCoverage": {"state": "partial", "reason": "node-without-agent", "refreshSeconds": 300, "nodes": 4, "nodesWithAgent": 3}}}`
	if err := json.Unmarshal([]byte(status), &view); err != nil {
		t.Fatal(err)
	}
	want, ok := fleetsrc.KernelEgressRow("k8s-production", []delivery.PactoView{view}, demoNow())
	if got := demoKernelEgressRow(); !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("demo row = %+v\nfleetsrc row = %+v", got, want)
	}
}
