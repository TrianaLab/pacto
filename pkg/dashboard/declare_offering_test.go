package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	offCluster = "c1"
	offRepo    = "registry.example/acme/pacto-inferred/idp-c1/services"
)

// offeringRev is producer idp-c1's inferred offering slug, verified by its
// key unless keyID is empty, citing object as its source object.
func offeringRev(slug, keyID, object string) fleet.RawRevision {
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: slug, Version: "0.0.0-inferred.2.0", Offering: slug}}
	fsys := fstest.MapFS{}
	if object != "" {
		fsys["source/object.yaml"] = &fstest.MapFile{Data: []byte(object)}
	}
	digest := "sha256:" + strings.Repeat("a", 63) + string(slug[0])
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: " + slug + "\n  version: 0.0.0-inferred.2.0\n  offering: " + slug + "\n"
	return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fsys}, Domain: "wrong.example/x", ResolvedRef: offRepo + "@" + digest, Digest: digest,
		Contributor: &fleet.Contributor{Producer: "idp-c1", KeyID: keyID, ReadAt: time.Now(), AsOf: time.Now(), Cluster: offCluster}}
}

const xrdObject = "apiVersion: example.io/v1\nkind: SourceKind\nmetadata:\n  name: xbuckets.example.io\n  generation: 2\n"

// offeringFleet is a fleet with a declared offering postgresql and the
// inferred offerings xbucket (verified, citing its source object), xqueue
// (no key verified it) and xtopic (verified, citing no source object).
func offeringFleet(t *testing.T) *fleet.Query {
	t.Helper()
	declared := fleet.RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5",
		Service: contract.Service{Name: "postgresql", Version: "16.1.0", Offering: "postgresql"}}, FS: fstest.MapFS{}},
		Domain: "registry.example/acme", ResolvedRef: "registry.example/acme/postgresql:16.1.0", Digest: "sha256:" + strings.Repeat("b", 64)}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{declared}}),
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true,
			Revisions: []fleet.RawRevision{offeringRev("xbucket", "installation", xrdObject), offeringRev("xqueue", "", xrdObject), offeringRev("xtopic", "installation", "")}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

var (
	xbucketKey = string(fleet.NewServiceKeyDomain("registry.example/acme/pacto-inferred/idp-c1", "xbucket"))
	xqueueKey  = string(fleet.NewServiceKeyDomain("registry.example/acme/pacto-inferred/idp-c1", "xqueue"))
	xtopicKey  = string(fleet.NewServiceKeyDomain("registry.example/acme/pacto-inferred/idp-c1", "xtopic"))
)

func inferredOfferingServer(t *testing.T, view func(context.Context, *fleet.Query) (*fleet.Query, error), d Declarer) string {
	t.Helper()
	q := offeringFleet(t)
	o := Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: viewerAuth(false, AuthState{SignedIn: true})}
	if d != nil {
		o.Declare = d
	}
	return serveOptions(t, o)
}

func TestEntitiesRouteDeclarationOfferingCase(t *testing.T) {
	base := inferredOfferingServer(t, nil, nil)
	var inferred, declared struct {
		Total         int `json:"total"`
		InferredTotal int `json:"inferredTotal"`
		Entities      []struct {
			Key              string         `json:"key"`
			InferredOffering map[string]any `json:"inferredOffering"`
		} `json:"entities"`
	}
	getJSON(t, base+"/api/fleet/entities?kinds=offering&declaration=inferred", http.StatusOK, &inferred)
	if inferred.Total != 3 || len(inferred.Entities) != 3 || inferred.Entities[0].InferredOffering == nil {
		t.Fatalf("inferred = %+v", inferred)
	}
	getJSON(t, base+"/api/fleet/entities?kinds=offering", http.StatusOK, &declared)
	if declared.Total != 1 || declared.InferredTotal != 3 || declared.Entities[0].InferredOffering != nil {
		t.Fatalf("declared = %+v", declared)
	}
}

func TestDashboardDeclareResolvesOffering(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := inferredOfferingServer(t, nil, d)
	var out DeclareAnswer
	postJSON(t, base+"/api/fleet/declare", map[string]any{"serviceKey": xbucketKey, "offeringAcceptance": map[string]any{"version": "1.0.0", "owner": "team-storage"}}, http.StatusOK, &out)
	w := d.plans[0]
	if w.Key != xbucketKey || w.Name != "xbucket" || w.Offering != "xbucket" || w.Cluster != offCluster || !strings.HasPrefix(w.From, offRepo+"@sha256:") ||
		w.Source == nil || w.Source.Kind != "SourceKind" || w.Source.Name != "xbuckets.example.io" || w.SourceUnread != "" {
		t.Fatalf("%+v", w)
	}
	postJSON(t, base+"/api/fleet/declare", map[string]any{"serviceKey": xtopicKey}, http.StatusOK, &out)
	if w := d.plans[1]; w.Source != nil || w.SourceUnread == "" || w.From == "" {
		t.Fatalf("%+v", w)
	}
}

// TestDashboardAcceptHiddenOffering404: an inferred offering the viewer may
// not see, and one no key verified, answer as a missing service.
func TestDashboardAcceptHiddenOffering404(t *testing.T) {
	d := &fakeDeclarer{write: true}
	hideAll := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	_, missing := call(t, http.MethodPost, inferredOfferingServer(t, nil, d)+"/api/fleet/declare", map[string]string{"serviceKey": "nope"})
	for name, c := range map[string]struct {
		base, key string
	}{"hidden": {inferredOfferingServer(t, hideAll, d), xbucketKey}, "unverified": {inferredOfferingServer(t, nil, d), xqueueKey}} {
		for path, body := range map[string]map[string]string{
			"/api/fleet/declare":        {"serviceKey": c.key},
			"/api/fleet/declare/accept": {"serviceKey": c.key, "intentDigest": "x", "mode": "sign"},
		} {
			code, got := call(t, http.MethodPost, c.base+path, body)
			if code != http.StatusNotFound || got["detail"] != missing["detail"] {
				t.Errorf("%s %s: %d %v", name, path, code, got)
			}
		}
	}
	if len(d.plans) != 0 {
		t.Fatalf("planned %+v", d.plans)
	}
}

// TestDashboardOfferingDetailAcceptsOnlyOnWritingHost: the accept-offering
// action is offered only by a host that writes.
func TestDashboardOfferingDetailAcceptsOnlyOnWritingHost(t *testing.T) {
	for name, c := range map[string]struct {
		d    Declarer
		want []string
	}{
		"writes":      {&fakeDeclarer{write: true}, []string{"open-graph", fleet.ActionAcceptOffering}},
		"plans only":  {&fakeDeclarer{}, []string{"open-graph"}},
		"no declarer": {nil, []string{"open-graph"}},
	} {
		var out struct {
			Actions []string `json:"actions"`
		}
		getJSON(t, inferredOfferingServer(t, nil, c.d)+"/api/fleet/entities/service?key="+url.QueryEscape(xbucketKey), http.StatusOK, &out)
		if !slices.Equal(out.Actions, c.want) {
			t.Errorf("%s: %v", name, out.Actions)
		}
	}
}

func TestDashboardAcceptOfferingRefusalCodesInEnum(t *testing.T) {
	f, _ := reflect.TypeFor[DeclareRefusal]().FieldByName("Code")
	enum := strings.Split(f.Tag.Get("enum"), ",")
	for _, c := range []string{"OfferingSchemaOpen", "OfferingSourceInvalid", "OfferingSourceUnverified", "OfferingMismatch", "VersionRequired",
		"ClaimRepositoryInferred", "ClusterNotReviewable", "NotAllowed", "DraftNotOffered", "ReviewFailed"} {
		if !slices.Contains(enum, c) {
			t.Errorf("DeclareRefusal.code has no %s", c)
		}
	}
}
