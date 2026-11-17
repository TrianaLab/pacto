package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const recordManifest = "kind: Deployment\nmetadata:\n  name: newsletter\nspec:\n  env:\n    - name: WORKER_URL\n"

// newsletterRecord is a valid inferred record for recordManifest: it names
// notification-worker (a fleet service), db (no fleet service), newsletter
// itself and one non-dependency field.
func newsletterRecord() []byte {
	sum := sha256.Sum256([]byte(recordManifest))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{
			{Path: "deploy/app.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{
			{Field: "dependencies[notification-worker]", Value: "notification-worker", Source: "deploy/app.yaml:18", Confidence: contract.ConfidenceLikely},
			{Field: "dependencies[db]", Value: "db", Source: "deploy/app.yaml:20", Confidence: contract.ConfidenceLikely},
			{Field: "dependencies[newsletter]", Value: "newsletter", Source: "deploy/app.yaml:21", Confidence: contract.ConfidenceLikely},
			{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely},
		}}
	return rec.Marshal()
}

// recordFleetOpts shapes the newsletter revision of recordFleet.
type recordFleetOpts struct {
	version  string // newsletter's pactoVersion
	record   []byte // its pacto.inferred.yaml; nil for none
	manifest string // deploy/app.yaml as the bundle holds it
	declare  bool   // newsletter declares notification-worker too
	observed bool   // newsletter calls notification-worker at runtime
}

// recordFleet: newsletter runs in namespace news, notification-worker in shop.
func recordFleet(t *testing.T, o recordFleetOpts) *Query {
	t.Helper()
	news := &contract.Contract{PactoVersion: o.version, Service: contract.Service{Name: "newsletter", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	if o.declare {
		news.Dependencies = []contract.Dependency{{Name: "notification-worker", Ref: "oci://x/notification-worker", Compatibility: "^1.0.0", Required: true}}
	}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(o.manifest)}}
	if o.record != nil {
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: o.record}
	}
	worker := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "notification-worker", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	k := SourceKindKubernetes
	col := &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: news, FS: fsys}, Digest: "sha256:news"},
			{Bundle: &contract.Bundle{Contract: worker, FS: fstest.MapFS{}}, Digest: "sha256:worker"},
		},
		Targets: []RawTarget{
			{Scope: "news", Kind: k, Name: "newsletter", Service: "newsletter", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "notification-worker", Service: "notification-worker", Compliance: StatusCompliant},
		},
	}
	if o.observed {
		col.Observed = []ObservedEdge{{From: "newsletter", To: "notification-worker", Count: 3}}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", "kubernetes", col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

var rNews, rWorker = NewServiceKey("newsletter"), NewServiceKey("notification-worker")

func validRecordOpts() recordFleetOpts {
	return recordFleetOpts{version: "2.5", record: newsletterRecord(), manifest: recordManifest}
}

// newsletterEdges is every relationship from newsletter, by provenance.
func newsletterEdges(s *FleetSnapshot) map[string][]Relationship {
	out := map[string][]Relationship{}
	for _, rel := range s.Relationships {
		if rel.FromService == rNews {
			out[rel.Provenance] = append(out[rel.Provenance], rel)
		}
	}
	return out
}

func TestBuildProjectsRecordDependencies(t *testing.T) {
	s := recordFleet(t, validRecordOpts()).SnapshotForSerialization()
	inf := newsletterEdges(s)[ProvenanceInferred]
	if len(inf) != 1 {
		t.Fatalf("inferred edges = %+v, want one (db resolves nothing, newsletter is itself)", inf)
	}
	rel := inf[0]
	want := []InferredSource{{Source: "k8s", Kind: "inferredRecord", Where: "deploy/app.yaml:18", Confidence: "likely"}}
	if rel.ToService != rWorker || !rel.Resolved || rel.Compatibility != contract.CompatibilityUnknown || rel.Type != RelationshipDependency ||
		rel.Confidence != "likely" || rel.To != "notification-worker" || !reflect.DeepEqual(rel.InferredSources, want) {
		t.Errorf("inferred edge = %+v", rel)
	}
	if ConsumesEdge(rel) {
		t.Error("an inferred edge must never be a consuming edge")
	}
	if s.Revisions[rel.FromRevision].Record == nil {
		t.Error("the revision must carry its record")
	}
	if s.Completeness != CompletenessComplete || hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) {
		t.Errorf("a valid record is no limitation: %s %+v", s.Completeness, s.Limitations)
	}
}

func TestBuildSkipsDeclaredRecordDependency(t *testing.T) {
	o := validRecordOpts()
	o.declare = true
	edges := newsletterEdges(recordFleet(t, o).SnapshotForSerialization())
	if len(edges[ProvenanceInferred]) != 0 || len(edges[ProvenanceDeclared]) != 1 {
		t.Errorf("edges = %+v, want only the declared one", edges)
	}
}

func TestBuildUnreadableRecordLimitation(t *testing.T) {
	o := validRecordOpts()
	o.record = []byte("format: nope\n")
	s := recordFleet(t, o).SnapshotForSerialization()
	if n := len(newsletterEdges(s)[ProvenanceInferred]); n != 0 {
		t.Errorf("a malformed record projected %d edges", n)
	}
	if !hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) || s.Completeness != CompletenessPartial {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
}

func TestBuildIgnoresRecordBelow25(t *testing.T) {
	o := validRecordOpts()
	o.version = "2.4"
	s := recordFleet(t, o).SnapshotForSerialization()
	if n := len(newsletterEdges(s)[ProvenanceInferred]); n != 0 || hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) {
		t.Errorf("a 2.4 record must be ignored: %d edges, %+v", n, s.Limitations)
	}
}

func TestBuildRecordDigestMismatchLimitation(t *testing.T) {
	o := validRecordOpts()
	o.manifest = recordManifest + "    - name: EXTRA\n"
	s := recordFleet(t, o).SnapshotForSerialization()
	if n := len(newsletterEdges(s)[ProvenanceInferred]); n != 0 {
		t.Errorf("a record whose sources do not match projected %d edges", n)
	}
	if !hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) || s.Completeness != CompletenessPartial {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
}

// The inferred edge feeds no declared reader: dependents, declared
// dependencies, summary tallies and the observed-only pairing read the same
// with and without the record.
func TestInferredEdgeStaysOutOfDeclaredReaders(t *testing.T) {
	without := validRecordOpts()
	without.record, without.observed = nil, true
	with := validRecordOpts()
	with.observed = true
	read := func(q *Query) (deps []ServiceKey, declared []Relationship, sum ServiceSummary, overview OverviewSummary, detail *ServiceDetailData) {
		g, err := q.Graph(GraphQuery{Service: "notification-worker", Direction: DirectionDependents})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range g.Nodes {
			deps = append(deps, n.Key)
		}
		view, err := q.GetService("newsletter")
		if err != nil {
			t.Fatal(err)
		}
		sum, _ = q.serviceSummary(view)
		d, err := q.EntityDetail(KindService, string(rNews))
		if err != nil {
			t.Fatal(err)
		}
		return deps, view.Dependencies, sum, q.Overview().Summary, d.Service
	}
	d1, v1, s1, o1, e1 := read(recordFleet(t, without))
	d2, v2, s2, o2, e2 := read(recordFleet(t, with))
	if !reflect.DeepEqual(d1, d2) || !reflect.DeepEqual(v1, v2) || !reflect.DeepEqual(s1, s2) {
		t.Errorf("declared readers changed:\n%v %+v %+v\n%v %+v %+v", d1, v1, s1, d2, v2, s2)
	}
	if o1.ObservedOnlyRelationships != 1 || o1.ObservedOnlyRelationships != o2.ObservedOnlyRelationships || s2.ObservedNotDeclared != 1 {
		t.Errorf("an inferred edge must not pair with an observed call: %d %d %d", o1.ObservedOnlyRelationships, o2.ObservedOnlyRelationships, s2.ObservedNotDeclared)
	}
	if !reflect.DeepEqual(e1.Dependencies, e2.Dependencies) {
		t.Errorf("detail dependencies changed: %+v %+v", e1.Dependencies, e2.Dependencies)
	}
}

func TestRestrictHidesInferredEdgesOfHiddenServices(t *testing.T) {
	q := recordFleet(t, validRecordOpts())
	r := q.Restrict(func(ns string) bool { return ns == "shop" })
	s := r.SnapshotForSerialization()
	for _, rel := range s.Relationships {
		if rel.FromService == rNews || strings.Contains(rel.To, "newsletter") {
			t.Errorf("a hidden service's edge survived: %+v", rel)
		}
	}
	w := s.Services[rWorker]
	if w == nil || w.HiddenDependents != 1 {
		t.Fatalf("notification-worker = %+v, want one hidden dependent", w)
	}
	g, err := r.Graph(GraphQuery{Service: "notification-worker", Direction: DirectionDependents})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.FromService == rNews {
			t.Errorf("the restricted graph names the hidden consumer: %+v", e)
		}
	}
}
