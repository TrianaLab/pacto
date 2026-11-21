package fleet_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestTraceRowDetailsNameNoHiddenNamespace: a viewer restricted to the first
// Jaeger candidate's namespace sees no other namespace anywhere in the row.
func TestTraceRowDetailsNameNoHiddenNamespace(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var ws []detect.Workload
	var svcs []detect.Service
	for _, ns := range []string{"obs-a", "obs-hidden"} {
		ws = append(ws, detect.Workload{ID: ns, Namespace: ns, Images: []string{"jaegertracing/all-in-one:1"}})
		svcs = append(svcs, detect.Service{Namespace: ns, Name: "jaeger-query", Ports: []detect.ServicePort{{Port: 16686}}})
	}
	rows := detect.ProviderRows(detect.Result{AsOf: at, Entries: detect.Traces(ws, svcs, "pacto-system")}, "detection", at)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return at }},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{
			{Scope: "obs-a", Kind: "kubernetes", Name: "web", Service: "web", Compliance: fleet.StatusCompliant, EvidenceAt: &at},
			{Scope: "obs-hidden", Kind: "kubernetes", Name: "api", Service: "api", Compliance: fleet.StatusCompliant, EvidenceAt: &at},
		}}),
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: rows, State: &fleet.SourceState{Status: fleet.SourceAvailable}}))
	if err != nil {
		t.Fatal(err)
	}
	r := fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "obs-a" }, nil).SnapshotForSerialization()
	i := slices.IndexFunc(r.Providers, func(p fleet.Provider) bool { return p.ID == "jaeger" })
	if i < 0 {
		t.Fatalf("no jaeger row in %+v", r.Providers)
	}
	b, _ := json.Marshal(r.Providers[i])
	if strings.Contains(string(b), "obs-hidden") || len(r.Providers[i].Objects) != 1 {
		t.Errorf("the row names a hidden namespace: %s", b)
	}
}
