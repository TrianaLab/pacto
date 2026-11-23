package fleetsrc

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestK8sSourceStampsCluster(t *testing.T) {
	cl := &fleet.Cluster{ID: "uid-a", Name: "eu-prod", NameSource: fleet.ClusterNameFromContext}
	col, err := NewK8sSource("uid-a", cl, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant"}}]}`),
	}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if col.Cluster != cl || len(col.Targets) != 1 {
		t.Fatalf("cluster = %+v, targets = %d", col.Cluster, len(col.Targets))
	}
}

const clusterFixtureBody = `schemaVersion: pacto.dev/fleet-targets/v1
cluster:
  id: uid-eu
  name: eu-prod
targets:
  - scope: prod
    kind: kubernetes
    name: payments
    service: payments
    compliance: Compliant
`

func TestTargetStateFileCluster(t *testing.T) {
	col, err := NewTargetStateFileSource("", writeFixture(t, "targets.yaml", clusterFixtureBody)).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := &fleet.Cluster{ID: "uid-eu", Name: "eu-prod", NameSource: fleet.ClusterNameFromFile}
	if !reflect.DeepEqual(col.Cluster, want) {
		t.Fatalf("cluster = %+v, want %+v", col.Cluster, want)
	}
	plain, err := NewTargetStateFileSource("", writeFixture(t, "plain.yaml", validFixture)).Collect(context.Background())
	if err != nil || plain.Cluster != nil {
		t.Fatalf("a file without a cluster block names no cluster: %+v, %v", plain.Cluster, err)
	}
}

func TestTargetStateFileClusterNeedsID(t *testing.T) {
	body := strings.Replace(clusterFixtureBody, "  id: uid-eu\n", "", 1)
	_, err := NewTargetStateFileSource("", writeFixture(t, "targets.yaml", body)).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cluster needs an id") {
		t.Fatalf("err = %v; want the missing id named", err)
	}
}

// clusterBody is a v5 evidence-source body: the listing server runs in uid-a
// and lists one record it accepted, one another cluster accepted and one with
// no stored cluster.
func clusterBody(t *testing.T, version string) string {
	t.Helper()
	target := func(subject, cluster string) evidenceingest.TargetDTO {
		return evidenceingest.TargetDTO{Subject: subject, Service: "payments", Producer: "prod-eu", Compliance: "Compliant", Cluster: cluster}
	}
	payload, err := json.Marshal(evidenceingest.TargetsResponse{
		SchemaVersion: version,
		Health:        evidenceingest.SourceHealth{Status: evidenceingest.HealthReady},
		Targets:       []evidenceingest.TargetDTO{target("here", "uid-a"), target("there", "uid-b"), target("old", "")},
		Cluster:       &evidenceingest.ClusterDTO{ID: "uid-a", Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestEvidenceHTTPSourceReadsCluster(t *testing.T) {
	srv := serveJSON(t, clusterBody(t, evidenceingest.TargetsSchemaVersionCluster))
	defer srv.Close()
	col, err := NewEvidenceHTTPSource("evidence-a", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if col.Cluster != nil {
		t.Fatalf("an evidence collection names no cluster of its own: %+v", col.Cluster)
	}
	want := map[string]*fleet.Cluster{
		"here":  {ID: "uid-a", Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel},
		"there": {ID: "uid-b"},
		"old":   nil,
	}
	for _, tg := range col.Targets {
		if !reflect.DeepEqual(tg.Cluster, want[tg.Name]) {
			t.Errorf("%s: cluster = %+v, want %+v", tg.Name, tg.Cluster, want[tg.Name])
		}
	}
	if len(col.Targets) != 3 {
		t.Fatalf("targets = %d", len(col.Targets))
	}
}

func TestEvidenceHTTPSourceAcceptsV5(t *testing.T) {
	srv := serveJSON(t, clusterBody(t, evidenceingest.TargetsSchemaVersionCluster))
	defer srv.Close()
	if _, err := NewEvidenceHTTPSource("", srv.URL).Collect(context.Background()); err != nil {
		t.Fatalf("a v5 body must be accepted: %v", err)
	}
	v6 := serveJSON(t, clusterBody(t, evidenceingest.TargetsSchemaVersionExtensions))
	defer v6.Close()
	if _, err := NewEvidenceHTTPSource("", v6.URL).Collect(context.Background()); err != nil {
		t.Fatalf("a v6 body must be accepted: %v", err)
	}
	future := serveJSON(t, clusterBody(t, "pacto.dev/evidence-source/v7"))
	defer future.Close()
	_, err := NewEvidenceHTTPSource("", future.URL).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), evidenceingest.TargetsSchemaVersionExtensions) {
		t.Fatalf("an unknown version is refused by name: %v", err)
	}
}

// TestLiveSourcesKnowTheirCluster: a live source names its cluster before it
// reads, so a failed read still lowers that cluster's status.
func TestLiveSourcesKnowTheirCluster(t *testing.T) {
	c := &fleet.Cluster{ID: "uid-eu", Name: "eu-prod"}
	var k, w fleet.ClusterSource = NewK8sSource("uid-eu", c, nil, nil), NewWorkloadSource("uid-eu-workloads", c, nil, nil)
	if k.Cluster() != c || w.Cluster() != c {
		t.Error("a live source names the cluster it was built for")
	}
}
