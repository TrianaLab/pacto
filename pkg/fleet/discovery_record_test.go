package fleet

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ownerFileField is the owner discovery reads from a catalog-info.yaml, as
// a record holds it: a source marker, never trust.
var ownerFileField = contract.Inferred{Field: "service.owner", Value: "team team-x", Source: "deploy/app.yaml:3", Confidence: contract.ConfidenceLikely,
	Class: contract.InferredClassOwnerFile, Outranks: []string{"deploy/app.yaml:4", "repository:.github/CODEOWNERS:4"}}

// recordWithFindings is recordOf(fields) carrying findings and dropped.
func recordWithFindings(t *testing.T, fields []contract.Inferred, findings []contract.InferredFinding, dropped int) []byte {
	t.Helper()
	rec, err := contract.ParseInferredRecord(recordOf(fields))
	if err != nil {
		t.Fatal(err)
	}
	rec.Findings, rec.FindingsDropped = findings, dropped
	return rec.Marshal()
}

func TestRevisionDetailDiscoveryFields(t *testing.T) {
	o := validRecordOpts()
	o.record = recordWithFindings(t, []contract.Inferred{ownerFileField},
		[]contract.InferredFinding{{Code: "DISCOVERY_CHART_DEPENDENCY_UNLOCKED", Source: "deploy/app.yaml:9", Message: "chart dependency 'redis' has no Chart.lock digest, not read"}}, 0)
	sd, rd := newsletterDetail(t, recordFleet(t, o))
	want := InferredField{Field: "service.owner", Value: "team team-x", Source: "deploy/app.yaml:3", Confidence: "likely", Class: "ownerFile",
		Outranks: []string{"deploy/app.yaml:4", "repository:.github/CODEOWNERS:4"}, Provenance: ProvenanceInferred}
	if len(rd.Inferred) != 1 || !reflect.DeepEqual(rd.Inferred[0], want) {
		t.Errorf("revision inferred = %+v", rd.Inferred)
	}
	if len(sd.InferredFields.Items) != 1 || !reflect.DeepEqual(sd.InferredFields.Items[0], want) {
		t.Errorf("service inferred = %+v", sd.InferredFields.Items)
	}
	f := rd.DiscoveryFindings
	if f.Total != 1 || f.Truncated || len(f.Items) != 1 || f.Items[0].Code != "DISCOVERY_CHART_DEPENDENCY_UNLOCKED" {
		t.Errorf("findings = %+v", f)
	}
}

func TestRevisionDetailFindingsBounded(t *testing.T) {
	findings := []contract.InferredFinding{{Code: contract.InferredFindingTruncated, Source: ".", Message: "discovery stopped after 5000 files; 7 files were not read, so results are partial"}}
	for i := range 100 {
		findings = append(findings, contract.InferredFinding{Code: "DISCOVERY_UNREADABLE", Source: fmt.Sprintf("f%03d.yaml", i), Message: fmt.Sprintf("f%03d.yaml could not be read", i)})
	}
	o := validRecordOpts()
	o.record = recordWithFindings(t, nil, findings, 20)
	_, rd := newsletterDetail(t, recordFleet(t, o))
	f := rd.DiscoveryFindings
	if len(f.Items) != MaxDiscoveryFindings || f.Items[0].Code != contract.InferredFindingTruncated || f.Total != 121 || !f.Truncated {
		t.Errorf("findings = %d items, first %q, total %d, truncated %v", len(f.Items), f.Items[0].Code, f.Total, f.Truncated)
	}
}

func TestRevisionDetailJSONKeepsFields(t *testing.T) {
	o := validRecordOpts()
	o.record = recordOf([]contract.Inferred{{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely}})
	_, rd := newsletterDetail(t, recordFleet(t, o))
	b, err := json.Marshal(rd)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"service", "version", "pactoVersion", "inferred", "inferredTotal", "identity", "provenance", "valid", "validation", "stages",
		"environments", "interfaces", "breaks", "configurations", "policies", "capabilities", "dependencies", "tools", "discoveryFindings"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("revision detail lacks %q: %s", k, b)
		}
	}
	if got := string(keys["discoveryFindings"]); got != `{"items":[],"total":0}` {
		t.Errorf("discoveryFindings = %s", got)
	}
	if got := string(keys["inferred"]); got != `[{"field":"workload","value":"service","source":"deploy/app.yaml:1","confidence":"likely","provenance":"inferred"}]` {
		t.Errorf("an inferred field without class or outranks changed shape: %s", got)
	}
}

// owners reads every owner a reader of the snapshot sees for newsletter.
func owners(t *testing.T, q *Query) string {
	t.Helper()
	s := q.snap.Services[rNews]
	rev := q.snap.Revisions[RevisionKey(revKeyOf(t, q, "newsletter"))]
	return fmt.Sprintf("service %+v claims %+v revision %+v", s.Owner, serviceOwnerClaims(q.snap, s), rev.Owner)
}

// An owner read from an owner file is inferred: neither with nor without a
// declared owner does it become the service's owner, a claim or a
// revision's owner.
func TestDiscoveredOwnerIsNotTheOwner(t *testing.T) {
	for _, noOwner := range []bool{false, true} {
		without := validRecordOpts()
		without.noOwner, without.record = noOwner, recordOf([]contract.Inferred{{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely}})
		with := without
		with.record = recordOf([]contract.Inferred{ownerFileField})
		a, b := owners(t, recordFleet(t, without)), owners(t, recordFleet(t, with))
		if a != b || strings.Contains(b, "team-x") {
			t.Errorf("noOwner=%v: an inferred owner changed the owner:\n%s\n%s", noOwner, a, b)
		}
	}
}

// A hand-edited record cannot raise trust: a class ownerFile on
// service.owner changes no owner claim and no provenance next to one
// without the class.
func TestRecordClassIsNotProvenance(t *testing.T) {
	plain := ownerFileField
	plain.Class = ""
	without, with := validRecordOpts(), validRecordOpts()
	without.noOwner, with.noOwner = true, true
	without.record, with.record = recordOf([]contract.Inferred{plain}), recordOf([]contract.Inferred{ownerFileField})
	qa, qb := recordFleet(t, without), recordFleet(t, with)
	if a, b := owners(t, qa), owners(t, qb); a != b {
		t.Errorf("the class changed an owner:\n%s\n%s", a, b)
	}
	_, ra := newsletterDetail(t, qa)
	_, rb := newsletterDetail(t, qb)
	if ra.Inferred[0].Provenance != ProvenanceInferred || rb.Inferred[0].Provenance != ProvenanceInferred || !reflect.DeepEqual(ra.Ownership, rb.Ownership) {
		t.Errorf("the class changed a provenance: %+v %+v / %+v %+v", ra.Inferred[0], ra.Ownership, rb.Inferred[0], rb.Ownership)
	}
}

// A dependency a catalog-info.yaml dependsOn names, projected from the
// record, is hidden with the revision that holds it.
func TestRestrictedRevisionHidesDiscoveredDependency(t *testing.T) {
	o := validRecordOpts()
	o.record = recordOf([]contract.Inferred{{Field: "dependencies[notification-worker]", Value: "notification-worker", Source: "deploy/app.yaml:18", Confidence: contract.ConfidencePossible}})
	r := recordFleet(t, o).Restrict(anyCluster(func(ns string) bool { return ns == "shop" }), anyClusterKind(nil))
	s := r.SnapshotForSerialization()
	if slices.ContainsFunc(s.Relationships, func(rel Relationship) bool { return rel.FromService == rNews }) {
		t.Error("the restricted view names a hidden revision's discovered dependency")
	}
	if w := s.Services[rWorker]; w == nil || w.HiddenDependents != 1 {
		t.Errorf("notification-worker = %+v, want the hidden dependent counted", w)
	}
}
