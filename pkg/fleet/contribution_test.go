package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const (
	ctbRoot = "registry.pacto.demo/demo"
	ctbRepo = ctbRoot + "/pacto-inferred/acme-idp/services"
	ctbFile = "export.json"
	ctbData = `{"ledger-sync":["ledger"]}`
)

// ctbRecord is a record naming one dependency per name, read from ctbFile.
func ctbRecord(deps ...string) *contract.InferredRecord {
	sum := sha256.Sum256([]byte(ctbData))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: ctbFile, Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{}}
	for _, d := range deps {
		rec.Fields = append(rec.Fields, contract.Inferred{Field: "dependencies[" + d + "]", Value: d, Source: ctbFile + ":1", Confidence: contract.ConfidenceLikely})
	}
	return rec
}

// ctbRev is name's contribution by producer under repo, holding raw as its
// pacto.yaml and rec as its record, made asOf.
func ctbRev(name, raw string, repo, producer string, rec *contract.InferredRecord, asOf time.Time) RawRevision {
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: "0.1.0"}}
	fsys := fstest.MapFS{ctbFile: {Data: []byte(ctbData)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	digest := "sha256:" + name + "-" + producer
	return RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fsys}, Domain: "wrong.example/x",
		ResolvedRef: repo + "@" + digest, Digest: digest,
		Contributor: &Contributor{Producer: producer, KeyID: "key-1", ReadAt: fixedNow(), AsOf: asOf}}
}

// ctbYAML is a contributed pacto.yaml declaring only name and version.
func ctbYAML(name string) string {
	return "pactoVersion: \"2.5\"\nservice:\n  name: " + name + "\n  version: 0.1.0\n"
}

// ctbDeclared is a declared revision of name at ctbRoot with a Kubernetes
// target in ns.
func ctbDeclared(name, ns string) (RawRevision, RawTarget) {
	digest := "sha256:" + name
	return RawRevision{Bundle: &contract.Bundle{Contract: extContract(name, "1.0.0"), FS: fstest.MapFS{}}, Domain: ctbRoot, Digest: digest},
		RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: name, Domain: ctbRoot, Digest: digest, Compliance: StatusCompliant}
}

// ctbFleet builds a fleet of the declared revisions and targets (k8s) and
// the contributed revisions (an Evidence Server source), with a 24h window.
func ctbFleet(t *testing.T, declared []string, contributed ...RawRevision) *FleetSnapshot {
	t.Helper()
	k8s := &Collection{}
	for _, d := range declared {
		name, ns, _ := strings.Cut(d, "@")
		rev, tgt := ctbDeclared(name, ns)
		k8s.Revisions, k8s.Targets = append(k8s.Revisions, rev), append(k8s.Targets, tgt)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("k8s", SourceKindKubernetes, k8s),
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: contributed}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func ledgerSync() RawRevision {
	return ctbRev("ledger-sync", ctbYAML("ledger-sync"), ctbRepo, "acme-idp", ctbRecord("ledger"), fixedNow().Add(-time.Hour))
}

func limitationsCoded(lims []Limitation, code string) []Limitation {
	return slices.DeleteFunc(slices.Clone(lims), func(l Limitation) bool { return l.Code != code })
}

func TestContributedRevisionIsInferredService(t *testing.T) {
	snap := ctbFleet(t, []string{"ledger@shop"}, ledgerSync())
	key := NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "ledger-sync")
	s := snap.Services[key]
	want := &ServiceContribution{Producer: "acme-idp", Repository: ctbRepo, Ref: ctbRepo + "@sha256:ledger-sync-acme-idp", KeyID: "key-1",
		ReadAt: ptrTime(fixedNow()), AsOf: ptrTime(fixedNow().Add(-time.Hour))}
	if s == nil || s.Declaration != DeclarationInferred || !reflect.DeepEqual(s.Contribution, want) || s.Workload != nil {
		t.Fatalf("service = %+v", s)
	}
	edges := slices.DeleteFunc(slices.Clone(snap.Relationships), func(r Relationship) bool { return r.FromService != key })
	if len(edges) != 1 || edges[0].ToService != NewServiceKeyDomain(ctbRoot, "ledger") || edges[0].Provenance != ProvenanceInferred ||
		edges[0].Compatibility != contract.CompatibilityUnknown {
		t.Fatalf("edges = %+v", edges)
	}
	if slices.Contains(snap.reverseDeps[NewServiceKeyDomain(ctbRoot, "ledger")], key) {
		t.Error("a contributed edge entered the declared index")
	}
	if snap.Sources[1].Status != SourceAvailable {
		t.Errorf("evidence source = %+v", snap.Sources[1])
	}
}

// TestContributionCitingUnlistedFileHasNoEdges: a record whose field cites
// a file inputs.sources leaves out is read as nothing; the contributed
// service stays, with no edge, and the record is named unreadable.
func TestContributionCitingUnlistedFileHasNoEdges(t *testing.T) {
	rec := ctbRecord("ledger")
	rec.Fields[0].Source = "other.json:1"
	snap := ctbFleet(t, []string{"ledger@shop"}, ctbRev("ledger-sync", ctbYAML("ledger-sync"), ctbRepo, "acme-idp", rec, fixedNow().Add(-time.Hour)))
	key := NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "ledger-sync")
	if s := snap.Services[key]; s == nil || s.Contribution == nil {
		t.Fatalf("service = %+v", s)
	}
	if slices.ContainsFunc(snap.Relationships, func(r Relationship) bool { return r.FromService == key }) {
		t.Errorf("an unreadable record made an edge: %+v", snap.Relationships)
	}
	if lims := limitationsCoded(snap.Limitations, LimitationInferredRecordUnreadable); len(lims) != 1 || !strings.Contains(lims[0].Message, "other.json") {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

// refusedRows is every pacto.yaml key a contribution may not declare, read
// from the current schema, so a new key is a row.
func refusedRows(t *testing.T) map[string]string {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(validation.SchemaBytes(), &schema); err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for k := range schema.Properties {
		if k != "pactoVersion" && k != "service" {
			rows[k] = ctbYAML("x") + k + ": null\n"
		}
	}
	for k := range schema.Properties["service"].Properties {
		if k != "name" && k != "version" {
			rows["service."+k] = ctbYAML("x") + "  " + k + ": null\n"
		}
	}
	if len(rows) < 20 || rows["service.external"] == "" || rows["dependencies"] == "" {
		t.Fatalf("rows = %v", rows)
	}
	return rows
}

func TestContributionDeclaringIsRefused(t *testing.T) {
	rows := refusedRows(t)
	rows["pacto.yaml"] = "service: [unclosed\n"
	rows["service"] = "pactoVersion: \"2.5\"\nservice: a-string\n"
	for field, raw := range rows {
		t.Run(field, func(t *testing.T) {
			snap := ctbFleet(t, nil, ctbRev("x", raw, ctbRepo, "acme-idp", ctbRecord(), fixedNow()))
			wantRefused(t, snap, "1 bundle acme-idp contributed was refused because it declares "+field+"; ")
		})
	}
	unread := ctbRev("x", "", ctbRepo, "acme-idp", ctbRecord(), fixedNow())
	delete(unread.Bundle.FS.(fstest.MapFS), "pacto.yaml")
	wantRefused(t, ctbFleet(t, nil, unread), "because it declares pacto.yaml")
	declRec := ctbRecord("ledger")
	declRec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDraft,
		Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}
	wantRefused(t, ctbFleet(t, nil, ctbRev("x", ctbYAML("x"), ctbRepo, "acme-idp", declRec, fixedNow())), "because a contribution carries no declaration")
}

func TestContributionRefProducerMismatchRefused(t *testing.T) {
	for _, repo := range []string{ctbRoot + "/pacto-inferred/other-idp/services", ctbRoot + "/ledger-sync"} {
		snap := ctbFleet(t, nil, ctbRev("x", ctbYAML("x"), repo, "acme-idp", ctbRecord(), fixedNow()),
			ctbRev("y", ctbYAML("y"), repo, "acme-idp", ctbRecord(), fixedNow()))
		wantRefused(t, snap, "2 bundles acme-idp contributed were refused, the first because the ref names no contribution of acme-idp")
	}
}

// wantRefused: snap holds no contributed service, one CONTRIBUTION_REFUSED
// with msg, and the evidence source stays available.
func wantRefused(t *testing.T, snap *FleetSnapshot, msg string) {
	t.Helper()
	for k, s := range snap.Services {
		if s.Contribution != nil {
			t.Errorf("service %s kept", k)
		}
	}
	lims := limitationsCoded(snap.Limitations, LimitationContributionRefused)
	if len(lims) != 1 || lims[0].Source != "evidence" || !strings.Contains(lims[0].Message, msg) {
		t.Errorf("refusals = %+v, want %q", lims, msg)
	}
	if st := snap.Sources[len(snap.Sources)-1]; st.Status != SourceAvailable {
		t.Errorf("evidence source = %+v", st)
	}
}

func TestContributionSupersededByContract(t *testing.T) {
	ledger := ctbRev("ledger", ctbYAML("ledger"), ctbRepo, "acme-idp", ctbRecord(), fixedNow())
	snap := ctbFleet(t, []string{"ledger@shop"}, ledger, ledgerSync())
	if snap.Services[NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "ledger")] != nil {
		t.Error("the contributed ledger was kept")
	}
	if lims := limitationsCoded(snap.Limitations, LimitationContributionSuperseded); len(lims) != 1 || lims[0].Message != "1 service acme-idp contributed has a contract; the contract wins" {
		t.Errorf("superseded = %+v", lims)
	}
	// A ledger in another registry supersedes nothing.
	other := ctbRev("ledger", ctbYAML("ledger"), "other.example/acme/pacto-inferred/acme-idp/services", "acme-idp", ctbRecord(), fixedNow())
	snap = ctbFleet(t, []string{"ledger@shop"}, other)
	if snap.Services[NewServiceKeyDomain("other.example/acme/pacto-inferred/acme-idp", "ledger")] == nil || len(limitationsCoded(snap.Limitations, LimitationContributionSuperseded)) != 0 {
		t.Errorf("services %v, limitations %+v", snap.Services, snap.Limitations)
	}
}

func TestRestrictRecountsSuperseded(t *testing.T) {
	snap := ctbFleet(t, []string{"ledger@shop", "billing@vault"},
		ctbRev("ledger", ctbYAML("ledger"), ctbRepo, "acme-idp", ctbRecord(), fixedNow()),
		ctbRev("billing", ctbYAML("billing"), ctbRepo, "acme-idp", ctbRecord(), fixedNow()),
		// The same service twice still counts once.
		ctbRev("billing", ctbYAML("billing"), ctbRepo, "acme-idp", ctbRecord(), fixedNow().Add(time.Minute)))
	q := NewQuery(snap)
	if lims := limitationsCoded(snap.Limitations, LimitationContributionSuperseded); len(lims) != 1 || !strings.HasPrefix(lims[0].Message, "2 services") {
		t.Fatalf("unrestricted = %+v", lims)
	}
	shop := q.Restrict(anyCluster(func(ns string) bool { return ns == "shop" }), nil).snap
	if lims := limitationsCoded(shop.Limitations, LimitationContributionSuperseded); len(lims) != 1 || !strings.HasPrefix(lims[0].Message, "1 service ") {
		t.Errorf("shop = %+v", lims)
	}
	none := q.Restrict(anyCluster(func(string) bool { return false }), nil).snap
	if lims := limitationsCoded(none.Limitations, LimitationContributionSuperseded); len(lims) != 0 {
		t.Errorf("none = %+v", lims)
	}
}

func TestContributionOlderThanWindowIsStale(t *testing.T) {
	old := ctbRev("old", ctbYAML("old"), ctbRepo, "acme-idp", ctbRecord(), fixedNow().Add(-40*24*time.Hour))
	undated := ctbRev("undated", ctbYAML("undated"), ctbRepo, "acme-idp", ctbRecord(), time.Time{})
	snap := ctbFleet(t, nil, old, undated)
	for _, name := range []string{"old", "undated"} {
		s := snap.Services[NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", name)]
		if s == nil || s.Contribution == nil || !s.Contribution.Stale {
			t.Errorf("%s = %+v", name, s)
		}
	}
	if s := snap.Services[NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "undated")]; s.Contribution.AsOf != nil {
		t.Errorf("undated asOf = %v", s.Contribution.AsOf)
	}
	// Without a window the age is checked against RecentEvidenceWindow, as
	// the producer row checks it.
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("e", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{old}}))
	if err != nil || !snap.Services[NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "old")].Contribution.Stale {
		t.Errorf("no window: %v", err)
	}
}

func contributedKey(name string) ServiceKey {
	return NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", name)
}

func TestContributedServiceActions(t *testing.T) {
	q := NewQuery(ctbFleet(t, []string{"ledger@shop"}, ledgerSync()))
	d, err := q.EntityDetail(KindService, string(contributedKey("ledger-sync")))
	if err != nil || !reflect.DeepEqual(d.Actions, []string{"open-graph"}) || d.Service == nil || d.Service.Contribution == nil ||
		d.Service.Contribution.Producer != "acme-idp" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if d, _ := q.EntityDetail(KindService, string(NewServiceKeyDomain(ctbRoot, "ledger"))); len(d.Actions) != 3 || d.Service.Contribution != nil {
		t.Errorf("declared actions = %v", d.Actions)
	}
}

// TestContributedServiceRefNamesProducer: a contributed service's reference
// names its producer, so a list can word it apart from a workload.
func TestContributedServiceRefNamesProducer(t *testing.T) {
	snap := ctbFleet(t, []string{"ledger@shop"}, ledgerSync())
	if ref := ServiceRef(snap.Services[contributedKey("ledger-sync")]); ref.ContributedBy != "acme-idp" || ref.Scope != "" {
		t.Errorf("contributed ref = %+v", ref)
	}
	if ref := ServiceRef(snap.Services[NewServiceKeyDomain(ctbRoot, "ledger")]); ref.ContributedBy != "" {
		t.Errorf("declared ref = %+v", ref)
	}
}

func TestContributedServiceDetailUsesServicePath(t *testing.T) {
	TestContributedServiceActions(t)
}

func TestTargetReportedByOnlyFromEvidence(t *testing.T) {
	tgt := RawTarget{Scope: "my-idp", Kind: "external", Name: "checkout-prod", Service: "checkout", Compliance: StatusCompliant}
	for kind, want := range map[string]string{SourceKindEvidenceHTTP: "my-idp", "memory": ""} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", kind, &Collection{Targets: []RawTarget{tgt}}))
		if err != nil {
			t.Fatal(err)
		}
		for k, got := range snap.Targets {
			d, err := NewQuery(snap).EntityDetail(KindTarget, string(k))
			if got.ReportedBy != want || err != nil || d.Target.ReportedBy != want {
				t.Errorf("%s: reportedBy %q, detail %+v %v", kind, got.ReportedBy, d, err)
			}
		}
	}
}

func TestContributedServiceHasNoCompliantStatus(t *testing.T) {
	q := NewQuery(ctbFleet(t, []string{"ledger@shop"}, ledgerSync()))
	if s := q.snap.Services[contributedKey("ledger-sync")]; s.Status != StatusUnknown {
		t.Errorf("status = %q", s.Status)
	}
	ex, err := q.Explain(string(contributedKey("ledger-sync")))
	if err != nil || !slices.ContainsFunc(ex.Reasons, func(r Reason) bool {
		return r.Code == LimitationServiceInferred && r.Source == "evidence" && strings.Contains(r.Message, "only acme-idp's contribution describes this service")
	}) {
		t.Errorf("explain = %+v, %v", ex, err)
	}
	g, err := q.Graph(GraphQuery{Service: string(contributedKey("ledger-sync")), Direction: DirectionDependencies})
	if err != nil || len(g.Meta.Limitations) == 0 || g.Meta.Limitations[0].Code != LimitationServiceInferred || g.Meta.Limitations[0].Source != "evidence" {
		t.Errorf("graph = %+v, %v", g, err)
	}
}

func TestContributedServiceStaysOutOfDeclaredTallies(t *testing.T) {
	q := NewQuery(ctbFleet(t, []string{"ledger@shop"}, ledgerSync()))
	ov := q.Overview()
	if ov.Summary.InferredServices != 1 || ov.Summary.InferredUnverified != 0 {
		t.Errorf("summary = %+v", ov.Summary)
	}
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil || !slices.ContainsFunc(list.Entities, func(e EntityRef) bool {
		return e.Key == string(contributedKey("ledger-sync")) && e.Declaration == DeclarationInferred && e.Scope == ""
	}) {
		t.Errorf("entities = %+v, %v", list, err)
	}
	if svc, _ := q.clusterMembers("x"); len(svc) != 0 {
		t.Errorf("cluster entities = %v", svc)
	}
}

func TestRestrictContributedLikeRegistryService(t *testing.T) {
	registryOnly := RawRevision{Bundle: &contract.Bundle{Contract: extContract("registry-only", "1.0.0"), FS: fstest.MapFS{}}, Domain: ctbRoot, Digest: "sha256:ro"}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{registryOnly}}),
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{ledgerSync()}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	for name, visible := range map[string]func(string, string) bool{
		"all":     func(string, string) bool { return true },
		"shop":    anyCluster(func(ns string) bool { return ns == "shop" }),
		"nothing": anyCluster(func(string) bool { return false }),
	} {
		r := q.Restrict(visible, nil).snap
		reg, ctb := r.Services[NewServiceKeyDomain(ctbRoot, "registry-only")] != nil, r.Services[contributedKey("ledger-sync")] != nil
		if reg != ctb {
			t.Errorf("%s: registry-only %v, contributed %v", name, reg, ctb)
		}
	}
}

func TestContributionIsDeterministic(t *testing.T) {
	build := func(revs ...RawRevision) *FleetSnapshot {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
			NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: revs}))
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	a, b := ledgerSync(), ctbRev("other", ctbYAML("other"), ctbRepo, "acme-idp", ctbRecord("ledger-sync"), fixedNow())
	if x, y := build(a, b), build(b, a); !reflect.DeepEqual(x.Services, y.Services) || x.SnapshotID != y.SnapshotID {
		t.Error("snapshots differ")
	}
}

// TestContributedServiceIgnoresTargets: a target keyed like a contributed
// service never makes it Compliant.
func TestContributedServiceIgnoresTargets(t *testing.T) {
	tgt := RawTarget{Scope: "acme-idp", Kind: "external", Name: "ledger-sync-prod", Service: "ledger-sync",
		Domain: ctbRoot + "/pacto-inferred/acme-idp", Compliance: StatusCompliant}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{ledgerSync()}, Targets: []RawTarget{tgt}}))
	if err != nil {
		t.Fatal(err)
	}
	if s := snap.Services[contributedKey("ledger-sync")]; s == nil || s.Contribution == nil || s.Status != StatusUnknown {
		t.Errorf("service = %+v", s)
	}
}

// TestContributionNewerRefusedIsStale: a record read in place of a refused
// newer version reads stale and says so.
func TestContributionNewerRefusedIsStale(t *testing.T) {
	refused := ledgerSync()
	refused.Contributor.NewerRefused = "2.0.0"
	c := ctbFleet(t, nil, refused).Services[contributedKey("ledger-sync")].Contribution
	if c == nil || !c.Stale || c.NewerRefused != "2.0.0" || !strings.Contains(c.Line(fixedNow()), ", stale (newer version 2.0.0 was refused)") {
		t.Errorf("contribution = %+v", c)
	}
}

// TestContributedEdgeInNeighborhood: a contributed service's inferred edge
// is walked by the neighbourhood from either end, and never enters the
// declared index.
func TestContributedEdgeInNeighborhood(t *testing.T) {
	q := NewQuery(ctbFleet(t, []string{"ledger@shop"}, ledgerSync()))
	ledger := NewServiceKeyDomain(ctbRoot, "ledger")
	for _, key := range []ServiceKey{contributedKey("ledger-sync"), ledger} {
		nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(key), Direction: DirectionBoth, Views: allViews()})
		if err != nil || !slices.ContainsFunc(nb.Edges, func(e NeighborhoodEdge) bool { return e.Provenance == ProvenanceInferred }) {
			t.Errorf("%s: neighborhood = %+v, %v", key, nb, err)
		}
	}
	if len(q.snap.reverseDeps[ledger]) != 0 {
		t.Errorf("declared index = %v", q.snap.reverseDeps[ledger])
	}
}
