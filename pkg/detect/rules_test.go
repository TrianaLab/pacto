package detect

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// workloads builds one workload per (namespace, image) pair, n times each.
func workloads(n int, ns, image string) []Workload {
	ws := make([]Workload, 0, n)
	for i := range n {
		ws = append(ws, Workload{ID: fmt.Sprintf("%s-%s-%d", ns, image, i), Namespace: ns, Images: []string{image}})
	}
	return ws
}

func TestRegistriesRankByNamespaces(t *testing.T) {
	var ws []Workload
	for _, ns := range []string{"a", "b", "c"} {
		ws = append(ws, workloads(1, ns, "ghcr.io/acme/app:1")...)
	}
	ws = append(ws, workloads(1, "d", "quay.io/prom/x:1")...)
	e := Registries(ws, false, true)
	if e.Value != "ghcr.io/acme" || e.Evidence != "in 3 of 4 namespaces, 75% of workloads" || e.Completeness != Complete {
		t.Fatalf("entry = %+v", e)
	}
	if want := []string{"ghcr.io/acme: 3 of 4 namespaces, 75% of workloads", "quay.io/prom: 1 of 4 namespaces, 25% of workloads"}; !slices.Equal(e.Details, want) {
		t.Errorf("details = %q", e.Details)
	}
	if e.Hint != "oci://ghcr.io/acme/<service>:<version>" {
		t.Errorf("hint = %q", e.Hint)
	}
}

func TestRegistriesOneNamespaceCannotWin(t *testing.T) {
	ws := workloads(50, "tenant", "attacker.io/x/app:1")
	for _, ns := range []string{"a", "b", "c"} {
		ws = append(ws, workloads(1, ns, "ghcr.io/acme/app:1")...)
	}
	if e := Registries(ws, false, true); e.Value != "ghcr.io/acme" {
		t.Errorf("value = %q, want ghcr.io/acme", e.Value)
	}
}

func TestNoHintWithoutMajority(t *testing.T) {
	cases := map[string]Entry{
		"registry one namespace": Registries(workloads(3, "a", "ghcr.io/acme/app"), false, true),
		"registry no majority": Registries(slices.Concat(workloads(1, "a", "ghcr.io/acme/app"), workloads(1, "b", "ghcr.io/acme/app"),
			workloads(1, "c", "quay.io/x/y"), workloads(1, "d", "quay.io/z/y")), false, true),
		"exposure one namespace": Exposure(true, nil, []Route{{Namespace: "a", ParentNamespace: "infra", ParentName: "gw"}}, nil, nil),
		"exposure no majority": Exposure(false, nil, nil, []Ingress{{Namespace: "a", Class: "nginx"}, {Namespace: "b", Class: "nginx"},
			{Namespace: "c", Class: "traefik"}, {Namespace: "d", Class: "haproxy"}}, nil),
	}
	for name, e := range cases {
		if e.Hint != "" || e.Completeness != Partial || !strings.Contains(e.Reason, "no clear majority") {
			t.Errorf("%s: entry = %+v", name, e)
		}
	}
}

func TestRegistriesScope(t *testing.T) {
	ws := slices.Concat(workloads(1, "b", "ghcr.io/acme/app"), workloads(1, "a", "ghcr.io/acme/app"))
	if e := Registries(ws, false, true); e.Scope != fleet.ScopeCluster || e.Namespaces != nil {
		t.Errorf("cluster-wide entry = %+v", e)
	}
	e := Registries(ws, false, false)
	if e.Scope != "" || !slices.Equal(e.Namespaces, []string{"a", "b"}) || e.Completeness != Partial || !strings.Contains(e.Reason, "watched namespaces") {
		t.Errorf("scoped entry = %+v", e)
	}
	var many []Workload
	for i := range MaxNamed + 1 {
		many = append(many, workloads(1, fmt.Sprintf("ns%d", i), "ghcr.io/acme/app")...)
	}
	if e := Registries(many, false, false); e.Scope != fleet.ScopeCluster || e.Namespaces != nil {
		t.Errorf("scoped entry over the bound = %+v", e)
	}
}

func TestRegistriesDockerHubLibrary(t *testing.T) {
	e := Registries(slices.Concat(workloads(1, "a", "nginx"), workloads(1, "b", "nginx:1.27"), workloads(1, "c", "registry.local:5000/app")), false, true)
	if e.Value != "docker.io/library" || !slices.Contains(e.Details, "registry.local:5000: 1 of 3 namespaces, 33% of workloads") {
		t.Errorf("entry = %+v", e)
	}
}

func TestRegistriesTieIsNoClearMajority(t *testing.T) {
	// Every meshed pod runs the app and the sidecar: both candidates hold
	// every namespace, so neither is a clear majority.
	var ws []Workload
	for i := range 4 {
		ns := fmt.Sprintf("ns%d", i)
		ws = append(ws, Workload{ID: ns, Namespace: ns, Images: []string{"cr.l5d.io/linkerd/proxy:2", "ghcr.io/acme/app:1"}})
	}
	if e := Registries(ws, false, true); e.Hint != "" || e.Completeness != Partial || e.Reason != noMajority {
		t.Errorf("entry = %+v", e)
	}
	routes := []Route{{Namespace: "a", ParentNamespace: "x", ParentName: "one"}, {Namespace: "a", ParentNamespace: "y", ParentName: "two"},
		{Namespace: "b", ParentNamespace: "x", ParentName: "one"}, {Namespace: "b", ParentNamespace: "y", ParentName: "two"}}
	if e := Exposure(true, nil, routes, nil, nil); e.Hint != "" || e.Completeness != Partial {
		t.Errorf("gateway tie = %+v", e)
	}
}

func TestRegistriesOfficialImagesGetNoHint(t *testing.T) {
	e := Registries(slices.Concat(workloads(1, "a", "redis"), workloads(1, "b", "postgres:16"), workloads(1, "c", "ghcr.io/acme/app")), false, true)
	if e.Value != officialImages || e.Hint != "" || e.Completeness != Complete {
		t.Errorf("entry = %+v", e)
	}
}

func TestPlaceBoundIsWhatAReaderKeeps(t *testing.T) {
	var ns []string
	for i := range fleet.MaxProviderObjects {
		ns = append(ns, fmt.Sprintf("ns%d", i))
	}
	var e Entry
	if place(&e, ns, nil); e.Scope != "" || len(e.Namespaces) != fleet.MaxProviderObjects {
		t.Errorf("at the bound = %+v", e)
	}
	e = Entry{}
	if place(&e, ns, []Object{{Kind: "Gateway", Namespace: "ns0", Name: "gw"}}); e.Scope != fleet.ScopeCluster || e.Namespaces != nil || e.Objects != nil {
		t.Errorf("over the bound = %+v", e)
	}
}

func TestExposureAndSignInScope(t *testing.T) {
	routes := []Route{{Namespace: "a", ParentNamespace: "infra", ParentName: "gw"}, {Namespace: "b", ParentNamespace: "infra", ParentName: "gw"}}
	ingresses := []Ingress{{Namespace: "a", Class: "nginx"}, {Namespace: "b", Class: "nginx"}}
	// Cluster-wide reads are cluster-scoped, "none found" included.
	for name, e := range map[string]Entry{
		"gateway": Exposure(true, nil, routes, nil, nil), "ingress": Exposure(false, nil, nil, ingresses, nil),
		"exposure none": Exposure(false, nil, nil, nil, nil), "sign-in none": SignIn(false, nil, nil, nil),
	} {
		if e.Scope != fleet.ScopeCluster || e.Namespaces != nil || e.Objects != nil {
			t.Errorf("cluster-wide %s = %+v", name, e)
		}
	}
	watched := []string{"a", "b", "c"}
	if e := Exposure(false, nil, nil, ingresses, watched); e.Scope != "" || !slices.Equal(e.Namespaces, []string{"a", "b"}) {
		t.Errorf("scoped ingress = %+v", e)
	}
	if e := Exposure(true, nil, routes, nil, watched); !slices.Equal(e.Namespaces, []string{"a", "b", "infra"}) ||
		!slices.Equal(e.Objects, []Object{{Kind: "Gateway", Namespace: "infra", Name: "gw"}}) {
		t.Errorf("scoped gateway = %+v", e)
	}
	for name, e := range map[string]Entry{"exposure none": Exposure(false, nil, nil, nil, watched), "sign-in none": SignIn(false, nil, nil, watched)} {
		if e.Scope != "" || !slices.Equal(e.Namespaces, watched) {
			t.Errorf("scoped %s = %+v", name, e)
		}
	}
}

func TestRegistriesUnparsableImageSkipped(t *testing.T) {
	ws := []Workload{{ID: "1", Namespace: "a", Images: []string{"Not A Ref!!"}}, {ID: "2", Namespace: "b", Images: []string{"ghcr.io/acme/x", "ghcr.io/acme/y"}}}
	if e := Registries(ws, false, true); e.Evidence != "in 1 of 1 namespaces, 100% of workloads" {
		t.Errorf("evidence = %q", e.Evidence)
	}
}

func TestRegistriesCapsCandidates(t *testing.T) {
	var ws []Workload
	for i := range MaxDetails + 3 {
		ws = append(ws, workloads(1, fmt.Sprintf("ns%d", i), fmt.Sprintf("r%d.io/x/app", i))...)
	}
	if e := Registries(ws, false, true); len(e.Details) != MaxDetails {
		t.Errorf("details = %d, want %d", len(e.Details), MaxDetails)
	}
}

func TestRegistriesSampledIsPartial(t *testing.T) {
	ws := slices.Concat(workloads(1, "a", "ghcr.io/acme/app"), workloads(1, "b", "ghcr.io/acme/app"))
	e := Registries(ws, true, true)
	if e.Completeness != Partial || e.Reason != fmt.Sprintf("sampled the first %d running pods", MaxPods) {
		t.Errorf("entry = %+v", e)
	}
}

func TestRegistriesNoWorkloadsIsUnknown(t *testing.T) {
	if e := Registries(nil, false, true); e.Completeness != Unknown || e.Kind != "registry" || e.Reason == "" {
		t.Errorf("entry = %+v", e)
	}
}

func node(providerID string, labels map[string]string, kernel string) Node {
	return Node{ProviderID: providerID, Labels: labels, KernelVersion: kernel}
}

func TestCloudFromProviderID(t *testing.T) {
	cases := []struct {
		node Node
		want string
	}{
		{node("gce://p/z/n", map[string]string{"cloud.google.com/gke-nodepool": "pool"}, ""), "GKE"},
		{node("gce://p/z/n", nil, ""), "GCE"},
		{node("aws:///us-east-1a/i-1", map[string]string{"eks.amazonaws.com/nodegroup": "ng"}, ""), "EKS"},
		{node("aws:///us-east-1a/i-1", map[string]string{"alpha.eksctl.io/cluster-name": "c"}, ""), "EKS"},
		{node("aws:///us-east-1a/i-1", nil, ""), "AWS"},
		{node("azure:///sub/x", map[string]string{"kubernetes.azure.com/cluster": "c"}, ""), "AKS"},
		{node("azure:///sub/x", nil, ""), "Azure"},
		{node("kind://docker/kind/kind-control-plane", nil, ""), "kind"},
		{node("openstack:///abc", nil, ""), "openstack"},
	}
	for _, c := range cases {
		if e := Cloud([]Node{c.node}); e.Value != c.want || e.Completeness != Complete || e.Scope != fleet.ScopeCluster || e.Evidence != "on 1 of 1 nodes" {
			t.Errorf("%s: entry = %+v, want %s", c.node.ProviderID, e, c.want)
		}
	}
	if e := Cloud([]Node{node("", nil, "")}); e.Completeness != Unknown {
		t.Errorf("empty providerID: %+v", e)
	}
	if e := Cloud(nil); e.Completeness != Unknown {
		t.Errorf("no nodes: %+v", e)
	}
}

func TestCloudMixedIsPartialMajority(t *testing.T) {
	e := Cloud([]Node{node("kind://a", nil, ""), node("kind://b", nil, ""), node("aws:///x", nil, "")})
	if e.Value != "kind" || e.Completeness != Partial || e.Reason == "" || !slices.Equal(e.Details, []string{"kind: 2 nodes", "AWS: 1 nodes"}) {
		t.Errorf("entry = %+v", e)
	}
}

func TestKernelDistribution(t *testing.T) {
	e := Kernel([]Node{node("", nil, "6.1.0-gke"), node("", nil, "6.1.5"), node("", nil, "5.15.0")})
	if e.Value != "kernel 6.1 on 2 of 3 nodes" || e.Evidence != "BTF likely (unconfirmed)" || e.Completeness != Partial ||
		e.Reason != "not confirmed on the node" || e.Scope != fleet.ScopeCluster || !slices.Equal(e.Details, []string{"6.1: 2 nodes", "5.15: 1 nodes"}) {
		t.Errorf("entry = %+v", e)
	}
	if e := Kernel(nil); e.Completeness != Unknown || e.Kind != "nodes" {
		t.Errorf("no nodes: %+v", e)
	}
}

func TestBTFFromNFDLabel(t *testing.T) {
	nfd := map[string]string{"feature.node.kubernetes.io/kernel-config.DEBUG_INFO_BTF": "true"}
	for _, kernel := range []string{"4.19.0", "garbage"} {
		if e := Kernel([]Node{node("", nfd, kernel)}); e.Evidence != "BTF likely (unconfirmed)" {
			t.Errorf("%s: evidence = %q", kernel, e.Evidence)
		}
	}
}

// btfCases are every kernel case the BTF rule reads.
var btfCases = map[string]string{"5.7.0": "unlikely", "5.8.0": "likely", "6.0": "likely", "garbage": "unknown", "5": "unknown", "5.x": "unknown"}

func TestBTFFromKernelVersion(t *testing.T) {
	for kernel, want := range btfCases {
		if e := Kernel([]Node{node("", nil, kernel)}); e.Evidence != "BTF "+want+" (unconfirmed)" {
			t.Errorf("%s: evidence = %q, want %s", kernel, e.Evidence, want)
		}
	}
	// One unlikely node outweighs an unknown one, which outweighs a likely one.
	if e := Kernel([]Node{node("", nil, "6.1"), node("", nil, "x"), node("", nil, "4.1")}); e.Evidence != "BTF unlikely (unconfirmed)" {
		t.Errorf("mixed evidence = %q", e.Evidence)
	}
}

func TestBTFNeverAvailable(t *testing.T) {
	for kernel := range btfCases {
		e := Kernel([]Node{node("", nil, kernel)})
		if strings.Contains(e.Value+e.Evidence, "available") {
			t.Errorf("%s: %q %q names available", kernel, e.Value, e.Evidence)
		}
	}
}

func TestKernelKeepsSafeCharacters(t *testing.T) {
	e := Kernel([]Node{node("", nil, "\x1b6.\u202e1-gke\x9b")})
	if e.Value != "kernel 6.1 on 1 of 1 nodes" {
		t.Errorf("value = %q", e.Value)
	}
}

func TestExposureGatewayHint(t *testing.T) {
	routes := []Route{
		{Namespace: "a", ParentNamespace: "infra", ParentName: "public"}, {Namespace: "b", ParentNamespace: "infra", ParentName: "public"},
		{Namespace: "b", ParentNamespace: "infra", ParentName: "public"}, {Namespace: "c", ParentNamespace: "c", ParentName: "own"},
	}
	e := Exposure(true, nil, routes, []Ingress{{Namespace: "x", Class: "nginx"}}, []string{"a", "b", "c", "infra"})
	if e.Value != "Gateway API" || e.Completeness != Complete || !slices.Equal(e.Namespaces, []string{"a", "b", "c", "infra"}) ||
		!slices.Equal(e.Objects, []Object{{Kind: "Gateway", Namespace: "infra", Name: "public"}}) {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.Hint, "parentRefs[0].name=public") || !strings.Contains(e.Hint, "parentRefs[0].namespace=infra") {
		t.Errorf("hint = %q", e.Hint)
	}
	if strings.Contains(e.Evidence, "infra") || e.Evidence != "HTTPRoutes in 2 of 3 namespaces attach to the same Gateway" {
		t.Errorf("evidence = %q", e.Evidence)
	}
}

func TestExposureOneNamespaceCannotWin(t *testing.T) {
	var routes []Route
	for range 20 {
		routes = append(routes, Route{Namespace: "tenant", ParentNamespace: "tenant", ParentName: "mine"})
	}
	for _, ns := range []string{"a", "b"} {
		routes = append(routes, Route{Namespace: ns, ParentNamespace: "infra", ParentName: "public"})
	}
	if e := Exposure(true, nil, routes, nil, []string{"tenant"}); !slices.Equal(e.Objects, []Object{{Kind: "Gateway", Namespace: "infra", Name: "public"}}) {
		t.Errorf("objects = %+v", e.Objects)
	}
}

func TestExposureIngressHint(t *testing.T) {
	e := Exposure(false, nil, nil, []Ingress{{Namespace: "a", Class: "nginx"}, {Namespace: "b", Class: "nginx"}, {Namespace: "b"}}, nil)
	if e.Value != "Ingress class nginx" || e.Evidence != "in 2 of 2 namespaces" || e.Completeness != Complete {
		t.Fatalf("entry = %+v", e)
	}
	for _, want := range []string{"dashboard.ingress.enabled=true", "dashboard.ingress.className=nginx", "hosts[0].host=<your-host>",
		"hosts[0].paths[0].path=/", "hosts[0].paths[0].pathType=Prefix"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q lacks %s", e.Hint, want)
		}
	}
	long := Exposure(true, nil, []Route{{Namespace: "a", ParentNamespace: "infra", ParentName: strings.Repeat("g", MaxRunes)},
		{Namespace: "b", ParentNamespace: "infra", ParentName: strings.Repeat("g", MaxRunes)}}, nil, nil)
	if long.Hint != "" || long.Completeness != Complete {
		t.Errorf("a hint over the bound is printed: %+v", long)
	}
}

func TestExposureNoneFound(t *testing.T) {
	for _, e := range []Entry{Exposure(true, nil, nil, nil, nil), Exposure(false, nil, nil, []Ingress{{Namespace: "a"}}, nil)} {
		if e.Value != "none found" || e.Completeness != Partial || e.Reason == "" {
			t.Errorf("entry = %+v", e)
		}
	}
}

func TestServedErrorIsUnknown(t *testing.T) {
	err := errors.New("forbidden")
	for _, e := range []Entry{Metrics(true, err), SignIn(true, []string{"a/dex"}, err, nil), Exposure(true, err, []Route{{Namespace: "a"}}, nil, nil)} {
		if e.Completeness != Unknown || !strings.Contains(e.Reason, "forbidden") || e.Value != "" {
			t.Errorf("entry = %+v", e)
		}
	}
}

func TestMetrics(t *testing.T) {
	if e := Metrics(true, nil); e.Value != "Prometheus Operator" || e.Evidence != "serves monitoring.coreos.com/v1; read passively" || e.Completeness != Complete {
		t.Errorf("served = %+v", e)
	}
	if e := Metrics(false, nil); e.Value != "none found" || e.Completeness != Complete {
		t.Errorf("not served = %+v", e)
	}
}

func TestSignInDexServiceNamesNamespace(t *testing.T) {
	e := SignIn(false, []string{"auth/dex", "auth/dex-web", "bad"}, nil, nil)
	if e.Value != "Dex" || e.Completeness != Complete || !slices.Equal(e.Namespaces, []string{"auth"}) || strings.Contains(e.Evidence, "auth") ||
		!slices.Equal(e.Objects, []Object{{Kind: "Service", Namespace: "auth", Name: "dex"}, {Kind: "Service", Namespace: "auth", Name: "dex-web"}}) {
		t.Errorf("entry = %+v", e)
	}
	if e := SignIn(true, nil, nil, nil); e.Value != "Dex" || e.Evidence != "serves dex.coreos.com/v1" {
		t.Errorf("served = %+v", e)
	}
	if e := SignIn(false, nil, nil, nil); e.Value != "none found" || e.Completeness != Partial || e.Reason != "an identity provider outside the cluster is not visible" {
		t.Errorf("none = %+v", e)
	}
}

func TestEncodeBounds(t *testing.T) {
	over := []Result{
		{Entries: make([]Entry, MaxEntries+1)},
		{Entries: []Entry{{Kind: "x", Details: make([]string, MaxDetails+1)}}},
		{Entries: []Entry{{Kind: "x", Namespaces: make([]string, MaxNamed+1)}}},
		{Entries: []Entry{{Kind: "x", Objects: make([]Object, MaxNamed+1)}}},
	}
	for i, r := range over {
		if _, err := Encode(r); err == nil {
			t.Errorf("case %d encoded past its bound", i)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Result{AsOf: asOf, Cluster: "c\u202e1", Entries: []Entry{Metrics(true, nil), UnknownEntry("cloud", "no nodes found"), Kernel([]Node{node("", nil, "6.1")})}}
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{out.Entries[0].Kind, out.Entries[1].Kind, out.Entries[2].Kind}
	if out.SchemaVersion != SchemaVersion || out.Cluster != "c1" || !slices.Equal(kinds, []string{"cloud", "metrics", "nodes"}) || in.Entries[0].Kind != "metrics" {
		t.Errorf("out = %+v", out)
	}
}
