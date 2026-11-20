package authn

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var clusterTestNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// clusterFleet builds one Kubernetes source per cluster id, each holding a
// target svc-<ns>-<id> in every namespace named for it.
func clusterFleet(t *testing.T, clusters map[string][]string) *fleet.Query {
	t.Helper()
	var srcs []fleet.Source
	for id, nss := range clusters {
		col := &fleet.Collection{Cluster: &fleet.Cluster{ID: id, Name: id + "-ctx", NameSource: fleet.ClusterNameFromContext}}
		for _, ns := range nss {
			col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: "svc-" + ns + "-" + id,
				Service: "svc-" + ns + "-" + id, EvidenceAt: &clusterTestNow})
		}
		srcs = append(srcs, fleet.NewMemorySource("k8s-"+id, fleet.SourceKindKubernetes, col))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return clusterTestNow }}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// singleClusterGoldenID is the snapshot id the Viewer gave alice on the
// one-cluster fleet of TestSingleClusterViewUnchanged, recorded before it
// reviewed per cluster.
const singleClusterGoldenID = "sha256:3c304287d6ea1d0ba9757ed0fc09537f21366836de7249f089ed1106b13f4666"

func TestSingleClusterViewUnchanged(t *testing.T) {
	q := clusterFleet(t, map[string][]string{"uid-eu": {"a", "b", "c"}})
	_, r, _ := testViewer()
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": r.review})).Query(context.Background(), alice, q)
	if err != nil || got.SnapshotID() != singleClusterGoldenID || !slices.Equal(services(got), []string{"svc-a-uid-eu"}) {
		t.Errorf("one cluster: %s %v %v", got.SnapshotID(), services(got), err)
	}
}

// clusterReviewer answers Pacto and workload reviews in one cluster: allow
// decides, and asked records every namespace asked.
type clusterReviewer struct {
	mu    sync.Mutex
	asked []string
	allow func(ctx context.Context, p Person, ns string) (bool, error)
}

func (c *clusterReviewer) review(ctx context.Context, p Person, ns string, _ ResourceAttributes) (bool, error) {
	c.mu.Lock()
	c.asked = append(c.asked, ns)
	c.mu.Unlock()
	return c.allow(ctx, p, ns)
}

func (c *clusterReviewer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.asked)
}

func readsNS(names ...string) func(context.Context, Person, string) (bool, error) {
	return func(_ context.Context, _ Person, ns string) (bool, error) { return slices.Contains(names, ns), nil }
}

func failing(err error) func(context.Context, Person, string) (bool, error) {
	return func(context.Context, Person, string) (bool, error) { return false, err }
}

// twoClusters is prod in eu and us, with shop in eu only.
func twoClusters(t *testing.T) *fleet.Query {
	return clusterFleet(t, map[string][]string{"uid-eu": {"prod", "shop"}, "uid-us": {"prod"}})
}

func access(s *fleet.FleetSnapshot) map[string]string {
	out := map[string]string{}
	for _, c := range s.Clusters {
		if c.Access != "" {
			out[c.ID] = c.Access
		}
	}
	return out
}

func TestViewerReviewsEachClusterWithItsReviewer(t *testing.T) {
	eu, us := &clusterReviewer{allow: readsNS("prod")}, &clusterReviewer{allow: readsNS()}
	v := NewViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review}))
	got, err := v.Query(context.Background(), alice, twoClusters(t))
	if err != nil || !slices.Equal(services(got), []string{"svc-prod-uid-eu"}) {
		t.Fatalf("a grant on prod in eu shows %v, %v", services(got), err)
	}
	s := got.SnapshotForSerialization()
	if !slices.Equal(slices.Sorted(slices.Values(eu.asked)), []string{"", "prod", "shop"}) || !slices.Equal(slices.Sorted(slices.Values(us.asked)), []string{"", "prod"}) {
		t.Errorf("eu asked %v, us asked %v", eu.asked, us.asked)
	}
	if a := access(s); len(a) != 1 || a["uid-us"] != fleet.ClusterNoAccess || !hasHidden(s, "uid-us", "you may read no namespace there") ||
		s.Completeness != fleet.CompletenessPartial {
		t.Errorf("access %v, completeness %s, limitations %+v", a, s.Completeness, s.Limitations)
	}
}

func TestViewerClusterlessInMultiIsNotReviewable(t *testing.T) {
	eu := clusterFleet(t, map[string][]string{"uid-eu": {"prod"}, "uid-us": {"prod"}}).SnapshotForSerialization()
	col := &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "loose", Service: "loose", EvidenceAt: &clusterTestNow}}}
	srcs := []fleet.Source{fleet.NewMemorySource("loose", fleet.SourceKindKubernetes, col)}
	for _, c := range eu.Clusters {
		srcs = append(srcs, fleet.NewMemorySource("k8s-"+c.ID, fleet.SourceKindKubernetes, &fleet.Collection{Cluster: &fleet.Cluster{ID: c.ID, Name: c.Name},
			Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "svc-" + c.ID, Service: "svc-" + c.ID, EvidenceAt: &clusterTestNow}}}))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return clusterTestNow }}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	everything := &clusterReviewer{allow: readsNS("prod")}
	v := NewViewer(byCluster(map[string]Review{"uid-eu": everything.review, "uid-us": everything.review}))
	got, err := v.Query(context.Background(), alice, fleet.NewQuery(snap))
	if err != nil || slices.Contains(services(got), "loose") || !slices.Contains(services(got), "svc-uid-eu") {
		t.Fatalf("clusterless item with two reviewers: %v, %v", services(got), err)
	}
	if s := got.SnapshotForSerialization(); !hasHidden(s, "", "Pacto cannot review you there") {
		t.Errorf("limitations %+v", s.Limitations)
	}
}

func TestViewerSingleReviewerTakesClusterless(t *testing.T) {
	r := &clusterReviewer{allow: readsNS("a")}
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": r.review})).Query(context.Background(), alice, viewerFleet(t, "a", "b"))
	if err != nil || !slices.Equal(services(got), []string{"svc-a"}) || slices.ContainsFunc(got.SnapshotForSerialization().Limitations, func(l fleet.Limitation) bool {
		return l.Code == fleet.LimitationClusterHidden
	}) {
		t.Errorf("the only reviewer decides clusterless items: %v, %v", services(got), err)
	}
}

func TestViewerFailedClusterHiddenOthersKept(t *testing.T) {
	eu, us := &clusterReviewer{allow: readsNS("prod", "shop")}, &clusterReviewer{allow: failing(errors.New("https://us.example: connection refused"))}
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review})).Query(context.Background(), alice, twoClusters(t))
	if err != nil || !slices.Equal(services(got), []string{"svc-prod-uid-eu", "svc-shop-uid-eu"}) {
		t.Fatalf("a failed cluster: %v, %v", services(got), err)
	}
	s := got.SnapshotForSerialization()
	if a := access(s); a["uid-us"] != fleet.ClusterReviewFailed || !hasHidden(s, "uid-us", "the review failed") || s.Completeness != fleet.CompletenessPartial {
		t.Errorf("access %v, limitations %+v", a, s.Limitations)
	}
	for _, l := range s.Limitations {
		if strings.Contains(l.Message, "us.example") {
			t.Errorf("a raw error reached a reader: %+v", l)
		}
	}
}

func TestViewerSlowClusterTimesOut(t *testing.T) {
	old := clusterReviewTimeout
	clusterReviewTimeout = 50 * time.Millisecond
	t.Cleanup(func() { clusterReviewTimeout = old })
	eu := &clusterReviewer{allow: readsNS("prod")}
	us := &clusterReviewer{allow: func(ctx context.Context, _ Person, _ string) (bool, error) { <-ctx.Done(); return false, ctx.Err() }}
	start := time.Now()
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review})).Query(context.Background(), alice, twoClusters(t))
	if err != nil || !slices.Equal(services(got), []string{"svc-prod-uid-eu"}) || time.Since(start) > 2*time.Second {
		t.Fatalf("a slow cluster: %v, %v after %s", services(got), err, time.Since(start))
	}
	if a := access(got.SnapshotForSerialization()); a["uid-us"] != fleet.ClusterReviewFailed {
		t.Errorf("access %v", a)
	}
	// The request's own context ending is the only error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewViewer(byCluster(map[string]Review{"uid-us": us.review})).Query(ctx, alice, twoClusters(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("an ended request: %v", err)
	}
}

func TestClusterReviewTimeoutDefaultIs5s(t *testing.T) {
	if clusterReviewTimeout != 5*time.Second {
		t.Errorf("clusterReviewTimeout = %s", clusterReviewTimeout)
	}
}

func TestViewerFailuresNotCached(t *testing.T) {
	broken := true
	us := &clusterReviewer{allow: func(context.Context, Person, string) (bool, error) {
		if broken {
			return false, errors.New("down")
		}
		return true, nil
	}}
	eu := &clusterReviewer{allow: readsNS("prod")}
	v := NewViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review}))
	if _, err := v.Query(context.Background(), alice, twoClusters(t)); err != nil {
		t.Fatal(err)
	}
	broken = false
	got, err := v.Query(context.Background(), alice, twoClusters(t))
	if err != nil || !slices.Contains(services(got), "svc-prod-uid-us") || len(access(got.SnapshotForSerialization())) != 0 {
		t.Errorf("a recovered cluster: %v %v, %v", services(got), access(got.SnapshotForSerialization()), err)
	}
}

func TestViewerCacheKeyHoldsCluster(t *testing.T) {
	eu, us := &clusterReviewer{allow: readsNS("prod")}, &clusterReviewer{allow: readsNS("prod")}
	v := NewViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review}))
	q := clusterFleet(t, map[string][]string{"uid-eu": {"prod"}, "uid-us": {"prod"}})
	if _, err := v.Query(context.Background(), alice, q); err != nil {
		t.Fatal(err)
	}
	if eu.count() != 2 || us.count() != 2 {
		t.Errorf("eu asked %v, us asked %v: each cluster reviews prod itself", eu.asked, us.asked)
	}
	if _, err := v.Query(context.Background(), alice, q); err != nil || eu.count() != 2 || us.count() != 2 {
		t.Errorf("a second request asked again: eu %v, us %v, %v", eu.asked, us.asked, err)
	}
	keys := 0
	for k := range v.answers {
		if strings.HasSuffix(k, "\x01prod") && (strings.HasPrefix(k, "uid-eu\x01") || strings.HasPrefix(k, "uid-us\x01")) {
			keys++
		}
	}
	if keys != 2 {
		t.Errorf("verdicts %v", v.answers)
	}
}

func TestViewerShortcutNeedsEveryCluster(t *testing.T) {
	yes := &clusterReviewer{allow: func(context.Context, Person, string) (bool, error) { return true, nil }}
	q := twoClusters(t)
	if got, err := NewViewer(byCluster(map[string]Review{"uid-eu": yes.review, "uid-us": yes.review})).Query(context.Background(), alice, q); err != nil || got != q {
		t.Errorf("a cluster-wide reader in every cluster gets the full snapshot: %v", err)
	}
	half := &clusterReviewer{allow: readsNS("prod")}
	q = clusterFleet(t, map[string][]string{"uid-eu": {"prod", "shop"}, "uid-us": {"prod", "dev"}})
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": yes.review, "uid-us": half.review})).Query(context.Background(), alice, q)
	if err != nil || got == q || !slices.Equal(services(got), []string{"svc-prod-uid-eu", "svc-prod-uid-us", "svc-shop-uid-eu"}) {
		t.Errorf("cluster-wide in eu only: %v, %v", services(got), err)
	}
}

// TestViewerShortcutNeedsEveryClusterOfAnyItem: a cluster only an Evidence
// Server target or a cluster-scope provider row carries has no reviewer, so a
// reader cluster-wide everywhere else still gets a restricted view that names
// it, never the full snapshot.
func TestViewerShortcutNeedsEveryClusterOfAnyItem(t *testing.T) {
	yes := &clusterReviewer{allow: func(context.Context, Person, string) (bool, error) { return true, nil }}
	eu := fleet.NewMemorySource("k8s-uid-eu", fleet.SourceKindKubernetes, &fleet.Collection{Cluster: &fleet.Cluster{ID: "uid-eu", Name: "eu"},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "svc-eu", Service: "svc-eu", EvidenceAt: &clusterTestNow}}})
	for what, src := range map[string]fleet.Source{
		"external target": fleet.NewMemorySource("ev", fleet.SourceKindEvidenceHTTP, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "ci",
			Kind: "external", Name: "ext-us", Service: "ext-us", EvidenceAt: &clusterTestNow, Cluster: &fleet.Cluster{ID: "uid-us", Name: "us"}}}}),
		"provider row": fleet.NewMemorySource("detect", "detect", &fleet.Collection{Providers: []fleet.Provider{{ID: "argo-us", Source: "detect",
			Name: "Argo CD", Evidence: "Argo CD found", Status: fleet.SourceAvailable, Provenance: fleet.ProvenanceInferred,
			Scope: fleet.ScopeCluster, Cluster: "us", ClusterID: "uid-us"}}}),
	} {
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return clusterTestNow }}, eu, src)
		if err != nil {
			t.Fatal(err)
		}
		q := fleet.NewQuery(snap)
		got, err := NewViewer(byCluster(map[string]Review{"uid-eu": yes.review})).Query(context.Background(), alice, q)
		if err != nil || got == q || !hasHidden(got.SnapshotForSerialization(), "uid-us", "Pacto cannot review you there") {
			t.Errorf("%s of a cluster with no reviewer: full %v, %v", what, got == q, err)
		}
		if slices.ContainsFunc(slices.Collect(maps.Values(got.SnapshotForSerialization().Targets)), func(tr *fleet.TargetRecord) bool { return tr.Cluster == "uid-us" }) {
			t.Errorf("%s: the unreviewed cluster's target shows", what)
		}
	}
}

// TestViewerFailedReviewWithoutItemsIsNamed: the only reviewer decides no
// namespace (a registry-only fleet) and its review fails; the answer names
// the failure, never a permission denial alone.
func TestViewerFailedReviewWithoutItemsIsNamed(t *testing.T) {
	bundle := &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "reg", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}, FS: fstest.MapFS{}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: bundle, Digest: "sha256:reg"}}}))
	if err != nil {
		t.Fatal(err)
	}
	r := &clusterReviewer{allow: failing(errors.New("timeout"))}
	got, err := NewViewer(one(r.review)).Query(context.Background(), alice, fleet.NewQuery(snap))
	checkReviewFailed(t, "a failed review with no namespace to decide", got, err)
}

// TestViewerShortcutNeedsEveryReviewer: a reviewer with nothing of its own
// in the snapshot, or none at all, keeps the full snapshot from a reader.
func TestViewerShortcutNeedsEveryReviewer(t *testing.T) {
	yes := &clusterReviewer{allow: func(context.Context, Person, string) (bool, error) { return true, nil }}
	q := twoClusters(t)
	// A reviewer with nothing of its own in the snapshot still decides
	// whether services without a namespace show.
	extra := &clusterReviewer{allow: readsNS()}
	reg := registryOnly(t)
	got, err := NewViewer(byCluster(map[string]Review{"": yes.review, "uid-ap": extra.review})).Query(context.Background(), alice, reg)
	if err != nil || got == reg || extra.count() != 1 || slices.Contains(services(got), "reg") {
		t.Errorf("a third reviewer: %v asked %v", err, extra.asked)
	}
	// A cluster with no reviewer keeps the full snapshot from a reader who
	// reads every other cluster.
	got, err = NewViewer(byCluster(map[string]Review{"uid-eu": yes.review})).Query(context.Background(), alice, q)
	if err != nil || got == q || !slices.Equal(services(got), []string{"svc-prod-uid-eu", "svc-shop-uid-eu"}) || access(got.SnapshotForSerialization())["uid-us"] != fleet.ClusterNotReviewable {
		t.Errorf("a cluster with no reviewer: %v, %v", services(got), err)
	}
	// No reviewer at all shows nothing.
	if got, err := NewViewer(byCluster(map[string]Review{})).Query(context.Background(), alice, q); err != nil || len(services(got)) != 0 {
		t.Errorf("no reviewer: %v, %v", services(got), err)
	}
}

// registryOnly is a service known only from a registry beside a target in a.
func registryOnly(t *testing.T) *fleet.Query {
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "reg", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}, FS: fstest.MapFS{}}, Digest: "sha256:reg"}},
		Targets: []fleet.RawTarget{{Scope: "a", Kind: fleet.SourceKindKubernetes, Name: "svc-a", Service: "svc-a"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestViewerRegistryOnlyBesideUnreviewableCluster: a reader every reviewer
// answered cluster-wide yes for sees a service without a namespace even when
// another cluster has no reviewer.
func TestViewerRegistryOnlyBesideUnreviewableCluster(t *testing.T) {
	bundle := &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "reg", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}, FS: fstest.MapFS{}}
	srcs := []fleet.Source{fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: bundle, Digest: "sha256:reg"}}})}
	for _, id := range []string{"uid-eu", "uid-us"} {
		srcs = append(srcs, fleet.NewMemorySource("k8s-"+id, fleet.SourceKindKubernetes, &fleet.Collection{Cluster: &fleet.Cluster{ID: id, Name: id},
			Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "svc-" + id, Service: "svc-" + id, EvidenceAt: &clusterTestNow}}}))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return clusterTestNow }}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	yes := &clusterReviewer{allow: func(context.Context, Person, string) (bool, error) { return true, nil }}
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": yes.review})).Query(context.Background(), alice, fleet.NewQuery(snap))
	if err != nil || !slices.Equal(services(got), []string{"reg", "svc-uid-eu"}) {
		t.Errorf("services %v, %v", services(got), err)
	}
}

func TestViewerSingleReviewerFailureIsPartialNotError(t *testing.T) {
	r := &clusterReviewer{allow: failing(errors.New("down"))}
	got, err := NewViewer(byCluster(map[string]Review{"uid-eu": r.review})).Query(context.Background(), alice, clusterFleet(t, map[string][]string{"uid-eu": {"a"}}))
	if err != nil || len(services(got)) != 0 {
		t.Fatalf("one failed cluster: %v, %v", services(got), err)
	}
	s := got.SnapshotForSerialization()
	if s.Completeness != fleet.CompletenessPartial || !hasHidden(s, "uid-eu", "cluster uid-eu-ctx: the review failed") || access(s)["uid-eu"] != fleet.ClusterReviewFailed {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
}

func TestViewerScopedReviewsWatchedInEveryCluster(t *testing.T) {
	eu, us := &clusterReviewer{allow: readsNS("prod")}, &clusterReviewer{allow: readsNS("prod")}
	v := NewScopedViewer(byCluster(map[string]Review{"uid-eu": eu.review, "uid-us": us.review}), []string{"prod"})
	got, err := v.Query(context.Background(), alice, twoClusters(t))
	if err != nil || !slices.Equal(services(got), []string{"svc-prod-uid-eu", "svc-prod-uid-us"}) {
		t.Fatalf("scoped: %v, %v", services(got), err)
	}
	if !slices.Equal(eu.asked, []string{"prod"}) || !slices.Equal(us.asked, []string{"prod"}) {
		t.Errorf("eu asked %v, us asked %v", eu.asked, us.asked)
	}
}

func TestViewerServiceAccountReviewedOnlyAtHome(t *testing.T) {
	yes := func(context.Context, Person, string) (bool, error) { return true, nil }
	for _, p := range []Person{{User: "system:serviceaccount:prod:deployer"}, {User: "n", Groups: []string{"system:nodes"}}} {
		eu, us := &clusterReviewer{allow: yes}, &clusterReviewer{allow: yes}
		m := map[string]Review{"uid-eu": eu.review, "uid-us": us.review}
		home := func(context.Context) (string, bool) { return "uid-eu", true }
		got, err := NewViewer(byCluster(m)).SignInCluster(home).Query(context.Background(), p, twoClusters(t))
		if err != nil || !slices.Equal(services(got), []string{"svc-prod-uid-eu", "svc-shop-uid-eu"}) || us.count() != 0 {
			t.Errorf("%s at home in eu: %v, us asked %v, %v", p.User, services(got), us.asked, err)
		}
		if a := access(got.SnapshotForSerialization()); a["uid-us"] != fleet.ClusterNotReviewable {
			t.Errorf("%s: access %v", p.User, a)
		}
		unknown := func(context.Context) (string, bool) { return "", false }
		got, err = NewViewer(byCluster(m)).SignInCluster(unknown).Query(context.Background(), p, twoClusters(t))
		if err != nil || len(services(got)) != 0 {
			t.Errorf("%s with an unknown home: %v, %v", p.User, services(got), err)
		}
		// Without a sign-in cluster set, the only reviewer is home; with two
		// there is none.
		if got, _ := NewViewer(byCluster(m)).Query(context.Background(), p, twoClusters(t)); len(services(got)) != 0 {
			t.Errorf("%s with no home set: %v", p.User, services(got))
		}
		one := map[string]Review{"uid-eu": eu.review}
		if got, _ := NewViewer(byCluster(one)).Query(context.Background(), p, clusterFleet(t, map[string][]string{"uid-eu": {"a"}})); len(services(got)) != 1 {
			t.Errorf("%s with one cluster: %v", p.User, services(got))
		}
	}
}

// one is the reviews of a single cluster whose items carry no cluster id.
func one(r Review) Reviews { return byCluster(map[string]Review{"": r}) }

// byCluster is the reviews m, the same on every request.
func byCluster(m map[string]Review) Reviews {
	return func(context.Context) map[string]Review { return m }
}

// checkReviewFailed checks a view whose only cluster's review failed: no
// error, nothing shown, partial, one CLUSTER_HIDDEN saying the review failed.
func checkReviewFailed(t *testing.T, what string, got *fleet.Query, err error) {
	t.Helper()
	if err != nil || got == nil {
		t.Fatalf("%s: %v", what, err)
	}
	s := got.SnapshotForSerialization()
	if len(s.Services) != 0 || s.Completeness != fleet.CompletenessPartial || !hasHidden(s, "", "review failed") {
		t.Errorf("%s: services %v, completeness %s, limitations %+v", what, services(got), s.Completeness, s.Limitations)
	}
}

// hasHidden reports a CLUSTER_HIDDEN limitation for cluster whose message
// holds text.
func hasHidden(s *fleet.FleetSnapshot, cluster, text string) bool {
	return slices.ContainsFunc(s.Limitations, func(l fleet.Limitation) bool {
		return l.Code == fleet.LimitationClusterHidden && l.Source == cluster && strings.Contains(l.Message, text)
	})
}
