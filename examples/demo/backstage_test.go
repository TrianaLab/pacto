package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoServesBackstageEntities: the demo's in-memory API serves the
// Backstage route offline, from its bundled fleet.
func TestDemoServesBackstageEntities(t *testing.T) {
	q := fleet.NewQuery(storyFleet(t))
	h := demoHandler(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backstage/entities", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/yaml") ||
		!strings.Contains(body, "apiVersion: backstage.io/v1alpha1") || !strings.Contains(body, "kind: Component") {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), body)
	}
}

// TestDemoRefusesBackstageEntitiesWhileASourceIsDown: the shipped demo fleet
// (the one app.wasm serves) holds the deliberately unreachable edge-cluster and
// the truncated registry-mirror, so the route refuses with 503 and names a
// source rather than publishing a catalog Backstage would orphan entities from.
func TestDemoRefusesBackstageEntitiesWhileASourceIsDown(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	h := demoHandler(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backstage/entities", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" ||
		!strings.Contains(body, "source edge-cluster is unavailable") || strings.Contains(body, "apiVersion:") {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Retry-After"), body)
	}
}

// TestDemoBackstageMarksInferredSources: over the demo fleet's inferred
// workloads and externals, an inferred Component names its workload and keeps
// owner unknown, every Resource says where Pacto learned it, and stripe names
// its inferred contract. The story fleet holds no inferred service, so this
// reads the shipped demo fleet through the mapper.
func TestDemoBackstageMarksInferredSources(t *testing.T) {
	es := backstage.Map(fleet.NewQuery(demoFleet(t)))
	inferred := 0
	for _, e := range es {
		ann := e.Metadata.Annotations
		switch {
		case e.Kind == "Resource":
			if ann[backstage.AnnotationSource] == "" {
				t.Errorf("resource %s names no source", e.Metadata.Name)
			}
			if ann[backstage.AnnotationExternalKey] == "stripe" && ann[backstage.AnnotationInferredContract] != "registry.pacto.demo/demo/pacto-inferred/stripe" {
				t.Errorf("stripe = %v", ann)
			}
		case e.Kind == "Component" && ann[backstage.AnnotationProvenance] == "inferred":
			inferred++
			if ann[backstage.AnnotationSource] != "workload" || !strings.Contains(ann[backstage.AnnotationWorkload], "/") || e.Spec.Owner != "unknown" {
				t.Errorf("inferred component %s = %v owner %q", e.Metadata.Name, ann, e.Spec.Owner)
			}
		}
	}
	if inferred == 0 {
		t.Error("the demo exports no inferred Component")
	}
}
