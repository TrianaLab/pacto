package fleet

import "testing"

func TestRoutes_Canonical(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"service", RouteForService("east/pay"), "/fleet/services/east%2Fpay"},
		{"revision", RouteForRevision("pay@sha256:x"), "/fleet/revisions/pay@sha256:x"},
		{"target", RouteForTarget("prod/k8s/app"), "/fleet/targets/prod%2Fk8s%2Fapp"},
		{"owner", RouteForOwner("team a"), "/fleet/owners/team%20a"},
		{"source", RouteForSource("oci"), "/fleet/sources/oci"},
		{"impact", RouteForImpact("pay"), "/fleet/impact/pay"},
		{"compare", RouteForCompare("pay"), "/fleet/compare/pay"},
		{"graph", RouteForGraph(KindService, "east/pay"), "/fleet/graph/service/east%2Fpay"},
		{"attention", RouteForAttention(), "/fleet/attention"},
		{"services", RouteForServices(), "/fleet/services"},
		{"overview", RouteForOverview(), "/fleet"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q want %q", c.name, c.got, c.want)
		}
	}
}

func TestRouteForEntity_AllKinds(t *testing.T) {
	cases := map[EntityKind]string{
		KindService:  "/fleet/services/pay",
		KindRevision: "/fleet/revisions/pay@x",
		KindTarget:   "/fleet/targets/prod%2Fk8s%2Fapp",
		KindOwner:    "/fleet/owners/team",
		KindSource:   "/fleet/sources/oci",
		"bogus":      "/fleet", // unknown kind falls back to the overview route
	}
	keys := map[EntityKind]string{
		KindService: "pay", KindRevision: "pay@x", KindTarget: "prod/k8s/app",
		KindOwner: "team", KindSource: "oci", "bogus": "whatever",
	}
	for kind, want := range cases {
		if got := RouteForEntity(kind, keys[kind]); got != want {
			t.Errorf("kind %q: got %q want %q", kind, got, want)
		}
	}
}

func TestRouteFilters(t *testing.T) {
	if got := RouteForServicesFilter("status", ""); got != "/fleet/services" {
		t.Errorf("empty value: got %q", got)
	}
	if got := RouteForServicesFilter("status", "NonCompliant"); got != "/fleet/services?status=NonCompliant" {
		t.Errorf("with value: got %q", got)
	}
	if got := RouteForAttentionFilter(""); got != "/fleet/attention" {
		t.Errorf("empty category: got %q", got)
	}
	if got := RouteForAttentionFilter("stale"); got != "/fleet/attention?category=stale" {
		t.Errorf("with category: got %q", got)
	}
}
