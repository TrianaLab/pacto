package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// jaegerDetection writes a detection holding a cloud and a jaeger entry. Its
// endpoint is not in-cluster, so Decode clears it and no test resolves a
// name.
func jaegerDetection(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Entries: []detect.Entry{
		{Kind: "cloud", Value: "kind", Completeness: detect.Complete},
		{Kind: "jaeger", Value: "Jaeger", Evidence: "query API on port 16686", Completeness: detect.Complete,
			Objects:  []detect.Object{{Kind: "Service", Namespace: "observability", Name: "jaeger-query"}},
			Endpoint: "http://jaeger-query.observability.invalid.svc:16686"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func liveFleet(t *testing.T, opts FleetOptions) *fleet.FleetSnapshot {
	t.Helper()
	opts.Now = func() time.Time { return detectedNow }
	snap, err := detectedService(t).Fleet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func rowsOf(snap *fleet.FleetSnapshot, source string) []string {
	var ids []string
	for _, p := range snap.Providers {
		if p.Source == source {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func hasSourceKind(snap *fleet.FleetSnapshot, kind string) bool {
	return slices.ContainsFunc(snap.Sources, func(s fleet.SourceState) bool { return s.Kind == kind })
}

// TestLiveEdgesOnlyOnDashboard: without LiveEdges a detected Jaeger is one
// inferred detection row and nothing reads it; with LiveEdges it is the row
// of its own edge source and the detection source no longer lists it.
func TestLiveEdgesOnlyOnDashboard(t *testing.T) {
	path := jaegerDetection(t)
	snap := liveFleet(t, FleetOptions{Detected: path})
	if got := rowsOf(snap, "detection"); !slices.Equal(got, []string{"cloud", "jaeger"}) || hasSourceKind(snap, fleet.SourceKindTraces) {
		t.Fatalf("rows %v, sources %+v", got, snap.Sources)
	}
	i := slices.IndexFunc(snap.Providers, func(p fleet.Provider) bool { return p.ID == "jaeger" })
	if p := snap.Providers[i]; p.Provenance != fleet.ProvenanceInferred || p.UsedFor[0] != "observed edges, read by the dashboard; nothing is turned on" {
		t.Fatalf("jaeger row = %+v", p)
	}

	snap = liveFleet(t, FleetOptions{Detected: path, LiveEdges: fleetsrc.NewLiveEdges(time.Now)})
	if got := rowsOf(snap, "detection"); !slices.Equal(got, []string{"cloud"}) {
		t.Fatalf("detection rows = %v", got)
	}
	if got := rowsOf(snap, "detected-jaeger"); !slices.Equal(got, []string{"jaeger"}) || !hasSourceKind(snap, fleet.SourceKindTraces) {
		t.Fatalf("jaeger rows = %v, sources %+v", got, snap.Sources)
	}
}

func TestDetectionReadErrorKeepsTheUnavailableSource(t *testing.T) {
	snap := liveFleet(t, FleetOptions{Detected: filepath.Join(t.TempDir(), "missing.json"), LiveEdges: fleetsrc.NewLiveEdges(time.Now)})
	st, _ := detectionState(t, snap)
	if st.Status != fleet.SourceUnavailable || st.Error == nil || st.Error.Code != "NOT_FOUND" || hasSourceKind(snap, fleet.SourceKindTraces) || len(snap.Sources) != 1 {
		t.Fatalf("sources = %+v", snap.Sources)
	}
}

func TestConfiguredSourceIDWinsOverAutoSource(t *testing.T) {
	snap := liveFleet(t, FleetOptions{Detected: jaegerDetection(t), LiveEdges: fleetsrc.NewLiveEdges(time.Now),
		ObservationSources: []ObservationSourceSpec{{ID: "detected-jaeger", Root: t.TempDir(), Path: "traces.json"}}})
	if hasSourceKind(snap, fleet.SourceKindTraces) {
		t.Fatalf("the auto source was built: %+v", snap.Sources)
	}
	if got := rowsOf(snap, "detection"); !slices.Equal(got, []string{"cloud", "jaeger"}) {
		t.Fatalf("detection rows = %v", got)
	}
}

// jaegerAt is a Jaeger source over a server answering links.
func jaegerAt(t *testing.T, links string) fleet.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(links)) }))
	t.Cleanup(srv.Close)
	e := detect.Entry{Kind: "jaeger", Value: "Jaeger", Completeness: detect.Complete, Endpoint: srv.URL,
		Objects: []detect.Object{{Kind: "Service", Namespace: "observability", Name: "jaeger-query"}}}
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Entries: []detect.Entry{e}}
	return fleetsrc.NewJaegerSource("detected-jaeger", res, e, fleetsrc.NewLiveEdges(time.Now), func() time.Time { return detectedNow })
}

// TestTraceEdgesFoldLikeMira: Build folds Jaeger's names like Mira's: a call
// between two declared services is an observed relationship, a caller no
// contract describes is an uncontracted caller.
func TestTraceEdgesFoldLikeMira(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "orders"), "orders")
	writeLocalBundle(t, filepath.Join(root, "payments"), "payments")
	snap := liveFleet(t, FleetOptions{LocalRoots: []string{root}, extraSources: []fleet.Source{
		jaegerAt(t, `{"data":[{"parent":"orders","child":"payments","callCount":3},{"parent":"ghost","child":"payments","callCount":1}]}`)}})
	if !slices.ContainsFunc(snap.Relationships, func(r fleet.Relationship) bool {
		return r.Provenance == fleet.ProvenanceObserved && string(r.FromService) == "orders" && string(r.ToService) == "payments" &&
			len(r.ObservedSources) > 0 && r.ObservedSources[0].Source == "detected-jaeger"
	}) {
		t.Fatalf("relationships = %+v", snap.Relationships)
	}
	if !slices.ContainsFunc(snap.UncontractedCallers, func(c fleet.UncontractedCaller) bool {
		return c.Caller == "ghost" && string(c.ToService) == "payments"
	}) {
		t.Fatalf("uncontracted callers = %+v", snap.UncontractedCallers)
	}
}

// TestTraceSourceMakesNoSnapshotPartial: a Jaeger that returned nothing is a
// partial advisory source; the snapshot stays complete.
func TestTraceSourceMakesNoSnapshotPartial(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "orders"), "orders")
	snap := liveFleet(t, FleetOptions{LocalRoots: []string{root}, extraSources: []fleet.Source{jaegerAt(t, `{"data":[]}`)}})
	i := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == "detected-jaeger" })
	if i < 0 || snap.Sources[i].Status != fleet.SourcePartial || !snap.Sources[i].Advisory || snap.Completeness == fleet.CompletenessPartial {
		t.Fatalf("sources %+v, completeness %s", snap.Sources, snap.Completeness)
	}
}
