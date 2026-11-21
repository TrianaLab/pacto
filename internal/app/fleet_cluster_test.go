package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

const (
	clusterA = "9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60"
	clusterB = "1b2c3d4e-5f60-4c2e-8f3a-9f1c4e2a0b1d"
)

func TestK8sSourceIDIsClusterUID(t *testing.T) {
	svc := k8sService("eu-prod-ctx")
	fake := &fakeFleetK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant","contract":{"serviceName":"svc-a"}}}]}`),
		identity: kubecluster.Identity{ID: clusterA, Name: "eu-prod-ctx", NameSource: kubecluster.NameFromContext},
	}
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return fake, nil }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotContext != "eu-prod-ctx" {
		t.Fatalf("identity read with context %q", fake.gotContext)
	}
	if src := sourceState(snap, clusterA); src == nil || src.Kind != fleet.SourceKindKubernetes {
		t.Fatalf("sources = %+v; want the live source keyed by the cluster UID", snap.Sources)
	}
	tg := snap.Targets[fleet.NewTargetKey("prod", "kubernetes", "svc-a")]
	if tg == nil || tg.Cluster != clusterA || tg.ClusterName != "eu-prod-ctx" || tg.ClusterRelation != fleet.ClusterRelationRunsIn {
		t.Fatalf("one cluster keeps the 3-part key and names the cluster: %+v", tg)
	}
	if len(snap.Clusters) != 1 || snap.Clusters[0].ID != clusterA {
		t.Fatalf("clusters = %+v", snap.Clusters)
	}
}

func sourceState(snap *fleet.FleetSnapshot, id string) *fleet.SourceState {
	if i := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == id }); i >= 0 {
		return &snap.Sources[i]
	}
	return nil
}

func writeTargetState(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "targets.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasLimitation(lims []fleet.Limitation, code, source string) bool {
	return slices.ContainsFunc(lims, func(l fleet.Limitation) bool { return l.Code == code && l.Source == source })
}

func TestK8sSourceIdentityUnreadKeepsContextID(t *testing.T) {
	// Alone: the context id stays and nothing is said about clusters, since
	// none is known.
	snap, err := k8sService("eu-prod-ctx").Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	if sourceState(snap, "eu-prod-ctx") == nil || len(snap.Clusters) != 0 || hasLimitation(snap.Limitations, fleet.LimitationClusterUnknown, "eu-prod-ctx") {
		t.Fatalf("sources %+v clusters %+v limitations %+v", snap.Sources, snap.Clusters, snap.Limitations)
	}
	// Beside a file that names a cluster and holds the same target, the live
	// source's clusterless target merges into it, which makes the live source
	// partial.
	file := writeTargetState(t, `schemaVersion: pacto.dev/fleet-targets/v1
cluster: {id: `+clusterA+`, name: eu-prod}
targets:
  - {scope: prod, kind: kubernetes, name: svc-a, service: svc-a, compliance: Compliant}
`)
	snap, err = k8sService("eu-prod-ctx").Fleet(context.Background(), FleetOptions{IncludeK8s: true, TargetStateFiles: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	live := sourceState(snap, "eu-prod-ctx")
	if live == nil || live.Status != fleet.SourcePartial || !hasLimitation(snap.Limitations, fleet.LimitationClusterUnknown, "eu-prod-ctx") {
		t.Fatalf("live source %+v limitations %+v; want partial with CLUSTER_UNKNOWN", live, snap.Limitations)
	}
}

// evidenceServer serves f's registry as an Evidence Server in cluster and
// returns its base URL.
func evidenceServer(t *testing.T, f evidenceFixture, cluster string) string {
	t.Helper()
	opts := f.serveOptions()
	opts.Cluster = &evidenceingest.ClusterDTO{ID: cluster, Name: "cluster-" + cluster[:4], NameSource: kubecluster.NameFromUID}
	return serveEvidence(t, f.svc, opts)
}

func evidenceTargets(snap *fleet.FleetSnapshot) []*fleet.TargetRecord {
	var out []*fleet.TargetRecord
	for _, tg := range snap.Targets {
		if tg.Kind == "external" {
			out = append(out, tg)
		}
	}
	return out
}

func TestTwoEvidenceServersSameProducerTwoTargets(t *testing.T) {
	// Two clusters, each with its own Evidence Server over its own registry,
	// accept a report about the same producer and subject.
	fa, fb := serveTestFixtures(t), serveTestFixtures(t)
	a, b := evidenceServer(t, fa, clusterA), evidenceServer(t, fb, clusterB)
	for base, f := range map[string]evidenceFixture{a: fa, b: fb} {
		if code := postFile(t, f.svc, base, f.envPath); code != http.StatusAccepted {
			t.Fatalf("accept status = %d", code)
		}
	}
	snap, err := fa.svc.Fleet(context.Background(), FleetOptions{EvidenceURLs: []string{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	got := evidenceTargets(snap)
	if len(got) != 2 {
		t.Fatalf("targets = %d; want one per cluster", len(got))
	}
	for _, cluster := range []string{clusterA, clusterB} {
		tg := snap.Targets[fleet.NewClusterTargetKey(cluster, "prod-eu", "external", "svc-a")]
		if tg == nil || tg.Cluster != cluster || tg.ClusterRelation != fleet.ClusterRelationReportedThrough {
			t.Fatalf("cluster %s: target %+v; want a 4-part key reported through it", cluster, tg)
		}
	}
}

func TestTwoEvidenceServersSharedRegistryOneTarget(t *testing.T) {
	// Two servers in two clusters list one registry; only A accepted the record.
	f := serveTestFixtures(t)
	a, b := evidenceServer(t, f, clusterA), evidenceServer(t, f, clusterB)
	if code := postFile(t, f.svc, a, f.envPath); code != http.StatusAccepted {
		t.Fatalf("accept status = %d", code)
	}
	snap, err := f.svc.Fleet(context.Background(), FleetOptions{EvidenceURLs: []string{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	got := evidenceTargets(snap)
	if len(got) != 1 {
		t.Fatalf("targets = %d; one record is one target", len(got))
	}
	tg := got[0]
	if tg.Cluster != clusterA || !slices.Equal(tg.Sources, []string{"evidence-http-1", "evidence-http-2"}) {
		t.Fatalf("target %+v; want cluster A with both sources", tg)
	}
	if len(snap.Clusters) != 1 || snap.Clusters[0].ID != clusterA {
		t.Fatalf("clusters = %+v; the listing server B adds no cluster", snap.Clusters)
	}
}

func TestEvidenceServeRejectsInvalidClusterID(t *testing.T) {
	f := serveTestFixtures(t)
	for name, c := range map[string]*evidenceingest.ClusterDTO{
		"id":          {ID: "-bad id"},
		"name":        {ID: clusterA, Name: strings.Repeat("n", 64), NameSource: kubecluster.NameFromContext},
		"name source": {ID: clusterA, Name: "eu-prod", NameSource: "guess"},
	} {
		opts := f.serveOptions()
		opts.Cluster = c
		if _, _, err := f.svc.buildEvidenceHost(opts); err == nil || !strings.Contains(err.Error(), "invalid cluster") {
			t.Errorf("%s: err = %v; want the start refused", name, err)
		}
	}
}

// TestFleetFingerprintsAddPromoterRows: the --fingerprints file names the
// promoters the workload source reports; a missing or invalid file is refused
// before any source is read.
func TestFleetFingerprintsAddPromoterRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fingerprints.yaml")
	if err := os.WriteFile(path, []byte("promoters:\n- product: Promoter\n  crd: stages.promoter.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := k8sService("ctx")
	fake := &fakeFleetK8sClient{disc: &k8sclient.CRDDiscovery{}, versions: map[string]string{"promoter.example.com": "v1"}}
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return fake, nil }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true, Fingerprints: path})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snap.Providers, func(p fleet.Provider) bool {
		return p.Evidence == "promotion owned by Promoter (API group promoter.example.com served)"
	}) {
		t.Errorf("providers = %+v", snap.Providers)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("promoters:\n- product: P\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{bad, filepath.Join(t.TempDir(), "missing.yaml")} {
		if _, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true, Fingerprints: p}); err == nil || !strings.Contains(err.Error(), "--fingerprints") {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}
