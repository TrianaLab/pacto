package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func backstageSvc(ns, name string, port int32) Service {
	return Service{Namespace: ns, Name: name, HasSelector: true, Ports: []ServicePort{{Name: "http", Port: port}}}
}

func TestBackstageDetection(t *testing.T) {
	app := func(ns string) Workload {
		return Workload{ID: ns + "/b", Namespace: ns, Images: []string{"ghcr.io/acme/backstage:1"}}
	}
	svc := Object{Kind: "Service", Namespace: "idp", Name: "backstage"}
	for _, tc := range []struct {
		name      string
		ws        []Workload
		svcs      []Service
		want      string // completeness
		endpoint  string
		reason    string
		details   []string
		objects   []Object
		namespace []string
	}{
		{name: "one Service", ws: []Workload{app("idp")}, svcs: []Service{backstageSvc("idp", "backstage", 7007), backstageSvc("idp", "web", 80)},
			want: Complete, endpoint: "http://backstage.idp.svc:7007", objects: []Object{svc}},
		{name: "two Services in one namespace", ws: []Workload{app("idp")}, svcs: []Service{backstageSvc("idp", "backstage-b", 7007), backstageSvc("idp", "backstage", 7007)},
			want: Complete, endpoint: "http://backstage.idp.svc:7007", details: []string{"1 more candidate; Pacto reads the first"},
			objects: []Object{svc, {Kind: "Service", Namespace: "idp", Name: "backstage-b"}}},
		{name: "three Services in one namespace", ws: []Workload{app("idp")},
			svcs: []Service{backstageSvc("idp", "backstage", 7007), backstageSvc("idp", "backstage-b", 7007), backstageSvc("idp", "backstage-c", 7007)},
			want: Complete, endpoint: "http://backstage.idp.svc:7007", details: []string{"2 more candidates; Pacto reads the first"},
			objects: []Object{svc, {Kind: "Service", Namespace: "idp", Name: "backstage-b"}, {Kind: "Service", Namespace: "idp", Name: "backstage-c"}}},
		{name: "two namespaces", ws: []Workload{app("idp"), app("aaa")}, svcs: []Service{backstageSvc("idp", "backstage", 7007), backstageSvc("aaa", "backstage", 7007)},
			want: Partial, reason: "2 Backstage candidates in several namespaces; set dashboard.backstage.url",
			objects: []Object{{Kind: "Service", Namespace: "aaa", Name: "backstage"}, svc}},
		{name: "no Service on 7007", ws: []Workload{app("idp")}, svcs: []Service{backstageSvc("idp", "backstage", 80), backstageSvc("other", "x", 7007)},
			want: Partial, reason: "no Service on port 7007 in front of it", namespace: []string{"idp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Backstage(tc.ws, tc.svcs)
			if e.Kind != "backstage" || e.Value != "Backstage" || e.Source != "running pods and Services" || e.Completeness != tc.want ||
				e.Endpoint != tc.endpoint || e.Reason != tc.reason || !reflect.DeepEqual(e.Details, tc.details) ||
				!reflect.DeepEqual(e.Objects, tc.objects) || !reflect.DeepEqual(e.Namespaces, tc.namespace) {
				t.Errorf("entry = %+v", e)
			}
			if e.Endpoint != "" && CheckEndpoint(e.Endpoint) != nil {
				t.Errorf("endpoint %q is not in-cluster", e.Endpoint)
			}
		})
	}
}

func TestBackstageImages(t *testing.T) {
	svcs := []Service{backstageSvc("idp", "backstage", 7007)}
	for img, want := range map[string]bool{
		"ghcr.io/acme/backstage:1": true, "ghcr.io/acme/backstage-app:1.2": true, "ghcr.io/acme/backstage-backend:1": true,
		"ghcr.io/acme/stagehand": false, "evil/backstage-x": false, "ghcr.io/acme/notbackstage": false,
	} {
		if got := Backstage([]Workload{{Namespace: "idp", Images: []string{img}}}, svcs).Value != NotDetected; got != want {
			t.Errorf("%s counts = %v, want %v", img, got, want)
		}
	}
}

func TestDetectedBackstageRowStaysProposal(t *testing.T) {
	e := Backstage([]Workload{{Namespace: "idp", Images: []string{"ghcr.io/acme/backstage:1"}}}, []Service{backstageSvc("idp", "backstage", 7007)})
	rows := ProviderRows(Result{Entries: []Entry{e}}, "detection", time.Time{})
	if len(rows) != 1 || rows[0].ID != "backstage" || rows[0].Name != "Backstage catalog" ||
		len(rows[0].UsedFor) != 1 || !strings.Contains(rows[0].UsedFor[0], "nothing is turned on") || !strings.Contains(rows[0].UsedFor[0], "inferred owners") {
		t.Errorf("rows = %+v", rows)
	}
}

// TestBackstageNotDetectedIsPartial: no Backstage image gives a partial "not
// detected" row naming the value that reads an external catalog, never an
// endpoint or an object.
func TestBackstageNotDetectedIsPartial(t *testing.T) {
	e := Backstage(nil, []Service{backstageSvc("idp", "backstage", 7007)})
	if e.Kind != "backstage" || e.Value != NotDetected || e.Completeness != Partial || e.Source != tracesSource ||
		!strings.Contains(e.Reason, "dashboard.backstage.url") || e.Endpoint != "" || len(e.Objects) != 0 || len(e.Namespaces) != 0 {
		t.Errorf("entry = %+v", e)
	}
}

// TestBackstageNotDetectedRowReadsNothing: the row of a catalog that was not
// detected never says its owners and golden paths are read.
func TestBackstageNotDetectedRowReadsNothing(t *testing.T) {
	missing := Backstage(nil, nil)
	found := Entry{Kind: "backstage", Value: "http://backstage.idp:7007", Completeness: Complete}
	rows := ProviderRows(Result{Entries: []Entry{missing, found}}, "detect", time.Now())
	if len(rows) != 2 || rows[0].UsedFor[0] != backstageNotDetectedUse || rows[1].UsedFor[0] != usedFor["backstage"] {
		t.Errorf("rows = %+v", rows)
	}
}
