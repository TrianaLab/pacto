package evidenceingest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// everyRecordBody is a v5 body holding a target of the listing cluster, one of
// another cluster, one with an unknown status, one with no cluster, a
// provisioning record and two usage records, one with a metric of no value.
const everyRecordBody = `{"schemaVersion":"pacto.dev/evidence-source/v5","generatedAt":"2026-11-19T10:00:00Z",
	 "health":{"status":"ready","subjects":1,"failedSubjects":0,"invalidArtifacts":0},"truncated":false,
	 "cluster":{"id":"c1","name":"prod","nameSource":"label"},
	 "targets":[
	  {"subject":"pay-prod","service":"pay","producer":"ci","compliance":"Compliant","coverage":{"evaluated":1,"required":1},
	   "contractRef":"oci://r/pay@sha256:aa","evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z","cluster":"c1"},
	  {"subject":"pay-b","service":"pay","digest":"sha256:bb","producer":"ci","compliance":"Compliant","coverage":{"evaluated":0,"required":0},
	   "evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z","cluster":"c2"},
	  {"subject":"pay-x","service":"pay","producer":"ci","compliance":"Bogus","coverage":{"evaluated":0,"required":0},
	   "evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z"},
	  {"subject":"pay-n","service":"pay","producer":"ci","compliance":"Compliant","coverage":{"evaluated":0,"required":0},
	   "evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z"}],
	 "provisioning":[{"subject":"s","service":"pay","contractRef":"oci://r/pay@sha256:cc","producer":"p","kind":"db","name":"main","outcome":"ok",
	   "evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z"}],
	 "usage":[{"subject":"s","service":"pay","contractRef":"oci://r/pay@sha256:dd","producer":"p","kind":"db","name":"main","outcome":"ok",
	   "metrics":[{"name":"m","value":1}],"evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z"},
	  {"subject":"s","service":"pay","contractRef":"oci://r/pay","producer":"p","kind":"db","name":"main","outcome":"ok",
	   "metrics":[{"name":"m"}],"evidenceAt":"2026-11-19T09:00:00Z","acceptedAt":"2026-11-19T09:01:00Z"}]}`

func everyRecord(t *testing.T) *fleet.Collection {
	t.Helper()
	col, err := TargetsCollection("ev", []byte(everyRecordBody))
	if err != nil {
		t.Fatal(err)
	}
	return col
}

func TestTargetsCollectionMapsTargets(t *testing.T) {
	col := everyRecord(t)
	if len(col.Targets) != 3 {
		t.Fatalf("targets = %d, want 3 (the bogus status is dropped)", len(col.Targets))
	}
	if c := col.Targets[0]; c.Kind != "external" || c.Digest != "sha256:aa" || c.Cluster == nil || c.Cluster.Name != "prod" {
		t.Errorf("first target = %+v", c)
	}
	if c := col.Targets[1]; c.Digest != "sha256:bb" || c.Cluster == nil || c.Cluster.ID != "c2" || c.Cluster.Name != "" {
		t.Errorf("another cluster's target = %+v", c)
	}
	if col.Targets[2].Cluster != nil {
		t.Errorf("a target with no cluster got %+v", col.Targets[2].Cluster)
	}
}

func TestTargetsCollectionMapsRecords(t *testing.T) {
	col := everyRecord(t)
	if len(col.Provisioning) != 1 || col.Provisioning[0].Digest != "sha256:cc" {
		t.Errorf("provisioning = %+v", col.Provisioning)
	}
	if len(col.Usage) != 1 || col.Usage[0].Digest != "sha256:dd" {
		t.Errorf("usage = %+v", col.Usage)
	}
	if len(col.Limitations) != 2 || col.Limitations[0].Code != fleet.LimitationSourceRecordInvalid || col.Limitations[0].Source != "ev" ||
		!strings.Contains(col.Limitations[1].Message, "1 usage records") {
		t.Errorf("limitations = %+v", col.Limitations)
	}
}

func TestTargetsCollectionDegraded(t *testing.T) {
	for name, tc := range map[string]struct{ health, want string }{
		"failed subjects": {`{"status":"ready","subjects":2,"failedSubjects":1,"invalidArtifacts":0}`, ""},
		"truncated":       {`{"status":"ready","subjects":1,"failedSubjects":0,"invalidArtifacts":0}`, "truncated"},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"schemaVersion":"pacto.dev/evidence-source/v2","generatedAt":"2026-11-19T10:00:00Z","health":` + tc.health +
				`,"truncated":true,"targets":[]}`
			col, err := TargetsCollection("ev", []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationSourcePartial || !strings.Contains(col.Limitations[0].Message, tc.want) {
				t.Errorf("limitations = %+v", col.Limitations)
			}
		})
	}
}

func TestTargetsCollectionRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":  `{"schemaVersion":"pacto.dev/evidence-source/v2","extra":1}`,
		"unknown schema": `{"schemaVersion":"pacto.dev/evidence-source/v9","generatedAt":"2026-11-19T10:00:00Z","health":{},"truncated":false,"targets":[]}`,
		"cut":            `{"schemaVersion":`,
	} {
		if _, err := TargetsCollection("ev", []byte(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTargetsCollectionMapsExtensions(t *testing.T) {
	body := `{"schemaVersion":"pacto.dev/evidence-source/v6","generatedAt":"2026-11-22T10:00:00Z",
	 "health":{"status":"ready","subjects":1,"failedSubjects":0,"invalidArtifacts":0},"truncated":false,
	 "cluster":{"id":"c1","name":"prod","nameSource":"label"},"targets":[],
	 "extensions":[
	  {"subject":"r/pay/prod/extension/backup","service":"pay","contractRef":"oci://r/pay@sha256:aa","producer":"acme-backup","producerKeyId":"k1",
	   "ref":"x.acme.io/backup","outcome":"Observed","present":false,"details":{"lastBackup":"2026-11-22T03:00:00Z"},
	   "evidenceAt":"2026-11-22T09:00:00Z","acceptedAt":"2026-11-22T09:01:00Z","cluster":"c1"},
	  {"subject":"r/pay/prod/extension/scan","service":"pay","digest":"sha256:bb","contractRef":"oci://r/pay@sha256:bb","producer":"acme-backup",
	   "ref":"x.acme.io/scan","outcome":"Failed","evidenceAt":"2026-11-22T09:00:00Z","acceptedAt":"2026-11-22T09:01:00Z","cluster":"c2"}]}`
	col, err := TargetsCollection("ev", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Extensions) != 2 || len(col.Targets) != 0 || len(col.Limitations) != 0 {
		t.Fatalf("collection = %+v", col)
	}
	at := func(v string) time.Time { tm, _ := time.Parse(time.RFC3339, v); return tm }
	accepted, absent := at("2026-11-22T09:01:00Z"), false
	want := []fleet.RawExtension{
		{Subject: "r/pay/prod/extension/backup", Service: "pay", Digest: "sha256:aa", Ref: "x.acme.io/backup", Present: &absent, Details: json.RawMessage(`{"lastBackup":"2026-11-22T03:00:00Z"}`),
			Outcome: "Observed", Producer: "acme-backup", ProducerKeyID: "k1", ObservedAt: at("2026-11-22T09:00:00Z"), AcceptedAt: &accepted,
			Cluster: &fleet.Cluster{ID: "c1", Name: "prod", NameSource: "label"}, Verified: true},
		{Subject: "r/pay/prod/extension/scan", Service: "pay", Digest: "sha256:bb", Ref: "x.acme.io/scan", Outcome: "Failed", Producer: "acme-backup",
			ObservedAt: at("2026-11-22T09:00:00Z"), AcceptedAt: &accepted, Cluster: &fleet.Cluster{ID: "c2"}, Verified: true},
	}
	if !reflect.DeepEqual(col.Extensions, want) {
		t.Errorf("extensions = %+v, want %+v", col.Extensions, want)
	}
}
