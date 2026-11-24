package evidenceingest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
)

// clusterRecord is a runtime record about subject, accepted in cluster.
func clusterRecord(subject, cluster string) Record {
	var env evidenceenvelope.Envelope
	env.Producer.ID = "prod-eu"
	env.EvidenceSet.Subject.Name = subject
	return Record{Envelope: env, Compliance: "Compliant", Service: "payments", Cluster: cluster}
}

// targetsBody serves h over recs and returns the raw GET /targets body.
func targetsBody(t *testing.T, recs []Record, cluster *ClusterDTO) string {
	t.Helper()
	h := NewHandler(NewAcceptor(extTrust, fakeResolver{}, healthStore{recs: recs, health: SourceHealth{Status: HealthReady}}, fixedNow), nil, nil).WithCluster(cluster)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + TargetsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

func TestTargetsResponseCarriesCluster(t *testing.T) {
	server := &ClusterDTO{ID: "uid-a", Name: "eu-prod", NameSource: "node-label"}
	h := NewHandler(NewAcceptor(nil, fakeResolver{}, healthStore{
		recs: []Record{clusterRecord("here", "uid-a"), clusterRecord("there", "uid-b")}, health: SourceHealth{Status: HealthReady},
	}, fixedNow), nil, nil).WithCluster(server)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var body TargetsResponse
	getJSON(t, srv.URL+TargetsPath, &body)
	if !reflect.DeepEqual(body.Cluster, server) {
		t.Fatalf("server cluster = %+v, want %+v", body.Cluster, server)
	}
	if len(body.Targets) != 2 || body.Targets[0].Cluster != "uid-a" || body.Targets[1].Cluster != "uid-b" {
		t.Fatalf("each target keeps the cluster that accepted it: %+v", body.Targets)
	}
}

func TestTargetsResponseOmitsUnsetCluster(t *testing.T) {
	body := targetsBody(t, []Record{clusterRecord("old", "")}, nil)
	if strings.Contains(body, `"cluster"`) {
		t.Fatalf("an unset cluster is omitted: %s", body)
	}
}

func TestTargetsResponseSchemaV5OnlyWithCluster(t *testing.T) {
	for name, tc := range map[string]struct {
		recs    []Record
		cluster *ClusterDTO
		want    string
	}{
		"no cluster":     {[]Record{clusterRecord("old", "")}, nil, TargetsSchemaVersion},
		"server cluster": {[]Record{clusterRecord("old", "")}, &ClusterDTO{ID: "uid-a"}, TargetsSchemaVersionCluster},
		"record cluster": {[]Record{clusterRecord("here", "uid-a")}, nil, TargetsSchemaVersionCluster},
	} {
		if body := targetsBody(t, tc.recs, tc.cluster); !strings.Contains(body, `"schemaVersion":"`+tc.want+`"`) {
			t.Errorf("%s: body %s, want schema %s", name, body, tc.want)
		}
	}
	// Provisioning alone stays v3 (TestTargetsSchemaV3WithoutUsage); a cluster
	// on top of it is v5.
	h, url, accept := usageHandler(t)
	accept(2, provisionedSet("ghcr.io/a/orders/production/configuration/db", fixedNow().Add(-time.Minute), provisioned("configuration", "db", "production")))
	h.WithCluster(&ClusterDTO{ID: "uid-a"})
	var body TargetsResponse
	getJSON(t, url+TargetsPath, &body)
	if body.SchemaVersion != TargetsSchemaVersionCluster || len(body.Provisioning) != 1 {
		t.Fatalf("provisioning with a cluster: schema %q, provisioning %d; want v5 and the record kept", body.SchemaVersion, len(body.Provisioning))
	}
}
