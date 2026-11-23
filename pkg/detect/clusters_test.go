package detect

import (
	"encoding/json"
	"testing"
	"time"
)

// TestClustersWireKeys pins the JSON keys of clusters.json, which the
// operator writes and older readers ignore.
func TestClustersWireKeys(t *testing.T) {
	c := Clusters{SchemaVersion: SchemaVersion, AsOf: time.Unix(0, 0).UTC(), Cluster: "uid", Total: 1,
		Sources: []InventorySource{{Source: "flux", Completeness: Partial, Reason: "labels not read", Namespaces: []string{"apps"}}},
		Candidates: []Candidate{{Source: "flux", Kind: "Secret", Namespace: "apps", Name: "eu", DisplayName: "eu",
			Labels: map[string]string{"env": "prod"}, UID: "u", UIDSource: "s", KubeConfig: &Object{Kind: "Secret", Namespace: "apps", Name: "eu"}}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schemaVersion":"1","asOf":"1970-01-01T00:00:00Z","cluster":"uid",` +
		`"sources":[{"source":"flux","completeness":"partial","reason":"labels not read","namespaces":["apps"]}],` +
		`"candidates":[{"source":"flux","kind":"Secret","namespace":"apps","name":"eu","displayName":"eu","labels":{"env":"prod"},` +
		`"uid":"u","uidSource":"s","kubeConfig":{"kind":"Secret","namespace":"apps","name":"eu"}}],"total":1}`
	if string(data) != want {
		t.Errorf("clusters.json = %s", data)
	}
}
