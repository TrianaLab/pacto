package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// clusterFake is a cluster with one Pacto, svc-a in prod; an empty uid reads
// as an identity Pacto may not read.
func clusterFake(uid string) *fakeFleetK8sClient {
	return &fakeFleetK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant","contract":{"serviceName":"svc-a"}}}]}`),
		identity: kubecluster.Identity{ID: uid, Name: uid, NameSource: kubecluster.NameFromContext},
	}
}

// contextService is a Service whose kubeconfig holds the contexts of
// clients, current the current one; hosts names each context's server, and
// a context with no host has a config that cannot be loaded.
func contextService(current string, clients map[string]*fakeFleetK8sClient, hosts map[string]string) *Service {
	svc := NewService(nil, nil)
	name := func(read string) string {
		if read == "" {
			return current
		}
		return read
	}
	svc.sys.kubeContext = func() string { return current }
	svc.sys.newK8s = func(read string) (k8sclient.K8sClient, error) {
		if c, ok := clients[name(read)]; ok {
			return c, nil
		}
		return nil, errors.New(`context "` + read + `" does not exist`)
	}
	svc.sys.contextConfig = func(read string) (*rest.Config, error) {
		if h, ok := hosts[name(read)]; ok {
			return &rest.Config{Host: h}, nil
		}
		return nil, errors.New("no config")
	}
	return svc
}

func TestContextsOnOneClusterReadOnce(t *testing.T) {
	svc := contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "eu-admin": clusterFake(clusterA)},
		map[string]string{"eu-prod": "https://eu", "eu-admin": "https://eu"})
	entries := svc.ResolveK8sContexts(context.Background(), false, []string{"eu-prod", "eu-admin"})
	if len(entries) != 2 || entries[0].Alias != "" || entries[1].Alias != "eu-prod" || entries[1].Host != "https://eu" {
		t.Fatalf("entries = %+v; want eu-admin an alias of eu-prod", entries)
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"eu-prod", "eu-admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sources) != 2 || sourceState(snap, clusterA) == nil || sourceState(snap, clusterA+workloadsSuffix) == nil || snap.Completeness != fleet.CompletenessComplete {
		t.Fatalf("sources %+v completeness %s; want the cluster read once", snap.Sources, snap.Completeness)
	}
}

// TestContextsOnOneServerSpellingCADifferentlyAreAliases: one context names
// the CA as a file, the other inline, and one writes the host with a trailing
// slash; they reach one server, so the second is an alias, never a conflict.
func TestContextsOnOneServerSpellingCADifferentlyAreAliases(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("PEM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := contextService("", map[string]*fakeFleetK8sClient{"admin": clusterFake(clusterA), "ro": clusterFake(clusterA)}, nil)
	svc.sys.contextConfig = func(read string) (*rest.Config, error) {
		if read == "admin" {
			return &rest.Config{Host: "https://prod", TLSClientConfig: rest.TLSClientConfig{CAFile: ca}}, nil
		}
		return &rest.Config{Host: "https://prod/", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("PEM")}}, nil
	}
	entries := svc.ResolveK8sContexts(context.Background(), false, []string{"admin", "ro"})
	if len(entries) != 2 || entries[1].Alias != "admin" || entries[1].Conflict != "" {
		t.Fatalf("entries = %+v; want ro an alias of admin", entries)
	}
}

func TestContextsSharingUIDOnDifferentServersArePartial(t *testing.T) {
	svc := contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "eu-clone": clusterFake(clusterA)},
		map[string]string{"eu-prod": "https://eu", "eu-clone": "https://clone"})
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"eu-prod", "eu-clone"}})
	if err != nil {
		t.Fatal(err)
	}
	st := sourceState(snap, "eu-clone")
	if st == nil || st.Status != fleet.SourceUnavailable || snap.Completeness != fleet.CompletenessPartial || sourceState(snap, clusterA) == nil {
		t.Fatalf("sources %+v completeness %s", snap.Sources, snap.Completeness)
	}
	if !hasLimitationText(snap.Limitations, fleet.LimitationClusterConflict, "eu-clone", "contexts eu-prod and eu-clone") {
		t.Fatalf("limitations = %+v; want a CLUSTER_CONFLICT naming both contexts", snap.Limitations)
	}
}

// hasLimitationText reports whether lims holds code from source with text in
// its message.
func hasLimitationText(lims []fleet.Limitation, code, source, text string) bool {
	for _, l := range lims {
		if l.Code == code && l.Source == source && strings.Contains(l.Message, text) {
			return true
		}
	}
	return false
}

func TestKAndContextOnOneClusterReadOnce(t *testing.T) {
	svc := contextService("eu-prod", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "eu-admin": clusterFake(clusterA)},
		map[string]string{"eu-prod": "https://eu", "eu-admin": "https://eu"})
	if e := svc.ResolveK8sContexts(context.Background(), true, []string{"eu-prod"}); len(e) != 1 || e[0].Name != "eu-prod" {
		t.Fatalf("--k8s --context <current> = %+v; want one entry", e)
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true, K8sContexts: []string{"eu-admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sources) != 2 || sourceState(snap, clusterA) == nil {
		t.Fatalf("sources = %+v; want the cluster read once", snap.Sources)
	}
}

func TestUnidentifiedContextAmongSeveralRefused(t *testing.T) {
	svc := contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "us-prod": clusterFake("")},
		map[string]string{"eu-prod": "https://eu", "us-prod": "https://us"})
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"eu-prod", "us-prod"}})
	if err != nil {
		t.Fatal(err)
	}
	st := sourceState(snap, "us-prod")
	if st == nil || st.Status != fleet.SourceUnavailable || st.Error != nil || sourceState(snap, "us-prod"+workloadsSuffix) != nil {
		t.Fatalf("sources = %+v; want us-prod refused alone", snap.Sources)
	}
	const want = "the cluster of context us-prod cannot be told apart from the others: Pacto could not read its identity"
	if !hasLimitationText(snap.Limitations, fleet.LimitationClusterUnknown, "us-prod", want) {
		t.Fatalf("limitations = %+v", snap.Limitations)
	}
	if len(snap.Targets) != 1 || snap.Completeness != fleet.CompletenessPartial {
		t.Fatalf("targets %v completeness %s; want only eu-prod's target", snap.Targets, snap.Completeness)
	}
	for _, tg := range snap.Targets {
		if tg.Cluster != clusterA {
			t.Fatalf("target %+v merged with an unidentified cluster", tg)
		}
	}
}

func TestSingleUnidentifiedContextKeepsToday(t *testing.T) {
	svc := contextService("", map[string]*fakeFleetK8sClient{"us-prod": clusterFake("")}, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"us-prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if st := sourceState(snap, "us-prod"); st == nil || st.Status != fleet.SourceAvailable || len(snap.Clusters) != 0 {
		t.Fatalf("sources %+v clusters %+v", snap.Sources, snap.Clusters)
	}
	if snap.Targets[fleet.NewTargetKey("prod", "kubernetes", "svc-a")] == nil {
		t.Fatalf("targets = %v; want the 3-part key", snap.Targets)
	}
}

// twoClusters is eu-prod on clusterA and us-prod on clusterB.
func twoClusters() (*Service, *fakeFleetK8sClient) {
	us := clusterFake(clusterB)
	return contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "us-prod": us},
		map[string]string{"eu-prod": "https://eu", "us-prod": "https://us"}), us
}

var twoContexts = []string{"eu-prod", "us-prod"}

func TestUnreachableContextServesLastCollectionStale(t *testing.T) {
	svc, us := twoClusters()
	opts := FleetOptions{K8sContexts: twoContexts, LongRunning: true, K8sNamespaces: []string{"prod"}}
	if _, err := svc.Fleet(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	us.down = errors.New("dial tcp: connection refused")
	snap, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	st := sourceState(snap, clusterB)
	if st == nil || st.Status != fleet.SourceStale || st.LastSuccessfulSync == nil || len(st.Namespaces) != 1 {
		t.Fatalf("source = %+v; want stale with its last sync and namespaces", st)
	}
	if snap.Targets[fleet.NewClusterTargetKey(clusterB, "prod", "kubernetes", "svc-a")] == nil || len(snap.Clusters) != 2 {
		t.Fatalf("targets %v clusters %+v; want the last targets kept", snap.Targets, snap.Clusters)
	}
}

func TestUnreachableContextIdentityFailureStaysStale(t *testing.T) {
	svc, us := twoClusters()
	opts := FleetOptions{K8sContexts: twoContexts, LongRunning: true}
	if _, err := svc.Fleet(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	us.down, us.identity = errors.New("dial tcp: i/o timeout"), kubecluster.Identity{}
	snap, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := sourceState(snap, clusterB); st == nil || st.Status != fleet.SourceStale {
		t.Fatalf("sources = %+v; want the remembered cluster stale", snap.Sources)
	}
	if hasLimitation(snap.Limitations, fleet.LimitationClusterUnknown, "us-prod") {
		t.Fatalf("a remembered cluster read as unknown: %+v", snap.Limitations)
	}
}

func TestRememberedStaleStateCarriesNoRawError(t *testing.T) {
	svc, us := twoClusters()
	opts := FleetOptions{K8sContexts: twoContexts, LongRunning: true}
	if _, err := svc.Fleet(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	us.down = errors.New(`Get "https://admin:t0ken@10.1.2.3:6443/apis": dial tcp 10.1.2.3:6443: connection refused`)
	snap, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(snap)
	if strings.Contains(string(body), "10.1.2.3") || strings.Contains(string(body), "t0ken") || sourceState(snap, clusterB).Error == nil {
		t.Fatalf("snapshot leaks the raw error or drops it: %s", body)
	}
}

func TestUnreachableContextOneShotIsPartial(t *testing.T) {
	for _, longRunning := range []bool{false, true} {
		svc, us := twoClusters()
		us.down = errors.New("dial tcp: connection refused")
		snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: twoContexts, LongRunning: longRunning})
		if err != nil {
			t.Fatal(err)
		}
		if st := sourceState(snap, clusterB); st == nil || st.Status != fleet.SourceUnavailable || snap.Completeness != fleet.CompletenessPartial {
			t.Fatalf("longRunning=%v: sources %+v completeness %s", longRunning, snap.Sources, snap.Completeness)
		}
	}
	// Alone and unreachable: partial, never empty.
	svc, us := twoClusters()
	us.down = errors.New("dial tcp: connection refused")
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"us-prod"}})
	if err != nil || snap.Completeness != fleet.CompletenessPartial {
		t.Fatalf("completeness %s, err %v", snap.Completeness, err)
	}
}

func TestTwoContextsTwoClustersFourPartKeys(t *testing.T) {
	svc, _ := twoClusters()
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: twoContexts})
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{clusterA, clusterB} {
		if tg := snap.Targets[fleet.NewClusterTargetKey(uid, "prod", "kubernetes", "svc-a")]; tg == nil || tg.Cluster != uid {
			t.Fatalf("targets = %v; want one svc-a per cluster", snap.Targets)
		}
	}
	if len(snap.Clusters) != 2 || snap.Completeness != fleet.CompletenessComplete {
		t.Fatalf("clusters %+v completeness %s", snap.Clusters, snap.Completeness)
	}
}

// TestContextWithoutClientOrConfig: a context the kubeconfig does not hold
// is an unavailable pair; one whose config cannot be loaded is still read,
// with no server to compare.
func TestContextWithoutClientOrConfig(t *testing.T) {
	svc := contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA)}, nil)
	entries := svc.ResolveK8sContexts(context.Background(), false, []string{"eu-prod", "ghost"})
	if entries[0].Config != nil || entries[0].UID != clusterA || entries[1].Err == nil {
		t.Fatalf("entries = %+v", entries)
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: []string{"eu-prod", "ghost"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ghost", "ghost" + workloadsSuffix} {
		if st := sourceState(snap, id); st == nil || st.Status != fleet.SourceUnavailable {
			t.Fatalf("source %s = %+v", id, st)
		}
	}
	if got := (FleetOptions{K8sContexts: []string{"eu-prod"}}).ConsumerSources(); len(got) != 1 || got[0] != "--context eu-prod (all namespaces)" {
		t.Fatalf("consumer sources = %q", got)
	}
}

// TestRefusedContextLimitationSurvivesRestrict pins that a restricted view
// keeps why a context was refused: its source read nothing, so nothing
// rebuilds the fixed-text limitation.
func TestRefusedContextLimitationSurvivesRestrict(t *testing.T) {
	cases := []struct {
		name, uid, code, source, text string
	}{
		{"unidentified", "", fleet.LimitationClusterUnknown, "us-prod", "the cluster of context us-prod cannot be told apart"},
		{"conflict", clusterA, fleet.LimitationClusterConflict, "us-prod", "contexts eu-prod and us-prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := contextService("", map[string]*fakeFleetK8sClient{"eu-prod": clusterFake(clusterA), "us-prod": clusterFake(tc.uid)},
				map[string]string{"eu-prod": "https://eu", "us-prod": "https://us"})
			snap, err := svc.Fleet(context.Background(), FleetOptions{K8sContexts: twoContexts})
			if err != nil {
				t.Fatal(err)
			}
			hideAll := func(string, string) bool { return false }
			view := fleet.NewQuery(snap).Restrict(hideAll, func(string, string, string) bool { return false }).Snapshot()
			if len(view.Targets) != 0 || !hasLimitationText(view.Limitations, tc.code, tc.source, tc.text) {
				t.Fatalf("targets %v limitations %+v; want the refusal kept", view.Targets, view.Limitations)
			}
		})
	}
}
