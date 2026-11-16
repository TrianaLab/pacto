package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// TestExternalDependencyIsNotAGap: an external dependency the fleet does not
// resolve carries its version and is never counted as unresolved, flagged for
// attention, explained as a gap or reported as declared-not-observed. A plain
// dependency that does not resolve still is. A fleet service that shares the
// external dependency's name is not it.
func TestExternalDependencyIsNotAGap(t *testing.T) {
	agent := &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: "agent", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Readiness: readyContract(),
		Dependencies: []contract.Dependency{
			{Name: "model", External: true, Version: "claude-sonnet-4-5", Required: true},
			{Name: "ghost", Ref: "oci://x/ghost:1.0.0", Compatibility: "^1.0.0", Required: true},
		}}
	other := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "other", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}
	// An unrelated fleet service that happens to share the external name.
	model := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "model", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: agent, FS: fstest.MapFS{}}, Digest: "sha256:agent"},
			{Bundle: &contract.Bundle{Contract: other, FS: fstest.MapFS{}}, Digest: "sha256:other"},
			{Bundle: &contract.Bundle{Contract: model, FS: fstest.MapFS{}}, Digest: "sha256:model"},
		},
		Observed: []ObservedEdge{{From: "agent", To: "other", Count: 1, FirstSeen: fixedNow().Add(-time.Hour), LastSeen: fixedNow()}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, src)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]Relationship{}
	for _, r := range snap.Relationships {
		if r.Provenance == ProvenanceDeclared {
			rels[r.To] = r
		}
	}
	m := rels["model"]
	want := Relationship{FromService: m.FromService, FromRevision: m.FromRevision, To: "model", Type: RelationshipDependency, Provenance: ProvenanceDeclared,
		Required: true, External: true, Version: "claude-sonnet-4-5", ToExternal: "model", DependencyBehavior: m.DependencyBehavior,
		Reason: "joined external model through normalized-name; this Pacto system does not deliver it"}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("model = %+v", m)
	}
	if g := rels["ghost"]; g.External || g.Reconciliation != ReconciliationDeclaredNotObserved || g.Reason != "no service in the fleet resolves this dependency ref" {
		t.Fatalf("ghost = %+v", g)
	}
	for k, deps := range snap.reverseDeps {
		if len(deps) > 0 {
			t.Fatalf("nothing depends on %s: %v", k, deps)
		}
	}
	checkExternalNotCounted(t, NewQuery(snap), rels["model"].FromService)
}

func checkExternalNotCounted(t *testing.T, q *Query, from ServiceKey) {
	t.Helper()
	if n := q.Overview().Summary.UnresolvedRelationships; n != 1 {
		t.Errorf("unresolved relationships = %d, want 1", n)
	}
	if n := len(q.unresolvedAttention()); n != 1 {
		t.Errorf("attention items = %d, want 1", n)
	}
	var sum ServiceSummary
	q.tallyServiceEdges(&sum, from)
	if sum.UnresolvedDeclared != 1 {
		t.Errorf("unresolved declared = %d, want 1", sum.UnresolvedDeclared)
	}
	res, err := q.Explain(string(from))
	if err != nil {
		t.Fatal(err)
	}
	gaps := 0
	for _, r := range res.Reasons {
		if r.Code == LimitationUnresolvedDep {
			gaps++
		}
	}
	if gaps != 1 {
		t.Errorf("explained gaps = %d, want 1: %+v", gaps, res.Reasons)
	}
}

func TestLockFromNoBundle(t *testing.T) {
	if lockFrom(RawRevision{}) != nil {
		t.Fatal("a revision with no bundle has no lock")
	}
}

// TestUnresolvedDependencyExternalReachesEveryPerspective: an external
// dependency keeps External and Version in the revision projection and the
// target projection. The service neighborhood leaves out a joined external:
// it names its external system, so it is not an unresolved dependency.
func TestUnresolvedDependencyExternalReachesEveryPerspective(t *testing.T) {
	agent := &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: "agent", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Readiness:    readyContract(),
		Dependencies: []contract.Dependency{{Name: "model", External: true, Version: "15", Required: true}}}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: agent, FS: fstest.MapFS{}}, Digest: "sha256:agent"}},
		Targets:   []RawTarget{{Scope: "prod", Kind: "k8s", Name: "agent-prod", Service: "agent", Digest: "sha256:agent", Compliance: StatusCompliant}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	for name, nq := range map[string]NeighborhoodQuery{
		"service":  {Kind: KindService, Key: "agent", Views: allViews()},
		"revision": {Kind: KindRevision, Key: string(revKeyFor(t, q, string(NewServiceKey("agent")))), Perspective: PerspectiveRevision, Direction: DirectionDependencies},
		"target":   {Kind: KindTarget, Key: string(targetKeyFor(t, q, "agent-prod")), Perspective: PerspectiveTarget, Direction: DirectionDependencies},
	} {
		nb, err := q.Neighborhood(nq)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "service" {
			if u := nb.UnresolvedDependencies.Items; len(u) != 0 {
				t.Errorf("service: a joined external is not unresolved: %+v", u)
			}
			continue
		}
		if u := nb.UnresolvedDependencies.Items; len(u) != 1 || !u[0].External || u[0].Version != "15" || u[0].Ref != "model" {
			t.Errorf("%s: unresolved = %+v", name, u)
		}
	}
}

func TestUnresolvedDepHelper(t *testing.T) {
	from := EntityRef{Kind: KindService, Key: "a"}
	rel := Relationship{To: "oci://x/db", FromRevision: "a@1", RequestedRef: "oci://x/db:15", Reason: "r", External: true, Version: "15"}
	want := UnresolvedDependency{From: from, Ref: "oci://x/db", SourceRevision: "a@1", RequestedRef: "oci://x/db:15", Reason: "r", External: true, Version: "15"}
	if got := unresolvedDep(from, rel); got != want {
		t.Errorf("unresolvedDep = %+v, want %+v", got, want)
	}
}

// extContract is a contract of service name at version declaring deps.
func extContract(name, version string, deps ...contract.Dependency) *contract.Contract {
	return &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}},
		Readiness: readyContract(), Dependencies: deps}
}

// extDep is a ref-less external dependency called name.
func extDep(name string) contract.Dependency {
	return contract.Dependency{Name: name, External: true, Version: "v-" + name}
}

// externalSnap builds a snapshot from cs, one revision each.
func externalSnap(t *testing.T, opts BuildOptions, cs ...*contract.Contract) *FleetSnapshot {
	t.Helper()
	var revs []RawRevision
	for _, c := range cs {
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + c.Service.Name + "-" + c.Service.Version})
	}
	opts.Now = fixedNow
	snap, err := Build(context.Background(), opts, NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// declaredTo is the declared dependency edge of service from on name.
func declaredTo(t *testing.T, snap *FleetSnapshot, from, name string) Relationship {
	t.Helper()
	for _, r := range snap.Relationships {
		if r.FromService == NewServiceKey(from) && r.To == name && r.Provenance == ProvenanceDeclared {
			return r
		}
	}
	t.Fatalf("no declared edge %s -> %s", from, name)
	return Relationship{}
}

func TestJoinCatalogAliasAcrossConsumers(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("checkout", "1.0.0", extDep("Stripe")), extContract("billing", "1.0.0", extDep("stripe-api")))
	rec := snap.Externals["stripe"]
	if len(snap.Externals) != 1 || rec == nil {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	q := NewQuery(snap)
	want := &ExternalRecord{Key: "stripe", Label: "Stripe", Class: externalcatalog.ClassVendorAPI, Aliases: []string{"stripe", "stripe-api"},
		Sources:      []ExternalSource{aliasSource(t, q, "billing", "stripe-api"), aliasSource(t, q, "checkout", "Stripe")},
		SourcesTotal: 2, ConsumersTotal: 2, Consumers: []ServiceKey{NewServiceKey("billing"), NewServiceKey("checkout")},
		Catalog:      &ExternalCatalogMatch{Origin: externalcatalog.OriginDefault, Digest: externalcatalog.DefaultDigest(), Entry: "stripe"},
		declarations: rec.declarations}
	if !reflect.DeepEqual(rec, want) {
		t.Fatalf("stripe = %+v, want %+v", rec, want)
	}
	rel := declaredTo(t, snap, "checkout", "Stripe")
	if rel.ToExternal != "stripe" || rel.Resolved || rel.ToService != "" ||
		rel.Reason != "joined external stripe through catalog-alias; this Pacto system does not deliver it" {
		t.Errorf("edge = %+v", rel)
	}
}

// aliasSource is the contract source a one-revision service svc declares as
// its first dependency, name, joined through a catalog alias.
func aliasSource(t *testing.T, q *Query, svc, name string) ExternalSource {
	t.Helper()
	return ExternalSource{Kind: ExternalSourceContract, Service: NewServiceKey(svc), Revision: revKeyFor(t, q, string(NewServiceKey(svc))),
		Dependency: name, Version: "v-" + name, Provenance: ProvenanceDeclared, Match: externalcatalog.MatchCatalogAlias}
}

func TestJoinNormalizedName(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("legacy-mq")), extContract("b", "1.0.0", extDep("Legacy_MQ")))
	rec := snap.Externals["legacy-mq"]
	if len(snap.Externals) != 1 || rec == nil || rec.Class != externalcatalog.ClassUnclassified || rec.Catalog != nil || rec.Label != "legacy-mq" || len(rec.Sources) != 2 {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	for _, src := range rec.Sources {
		if src.Match != externalcatalog.MatchNormalizedName {
			t.Errorf("match = %q", src.Match)
		}
	}
}

func TestJoinHostnameRegistrableDomain(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("mq.corp-legacy.co.uk")))
	rec := snap.Externals["host/corp-legacy.co.uk"]
	if rec == nil || rec.Sources[0].Match != externalcatalog.MatchHost || rec.Label != "mq.corp-legacy.co.uk" {
		t.Fatalf("externals = %+v", snap.Externals)
	}
}

func TestRefExternalNeverJoins(t *testing.T) {
	dep := contract.Dependency{Name: "stripe", External: true, Ref: "oci://ghcr.io/acme/stripe:1.0.0"}
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", dep))
	rel := declaredTo(t, snap, "a", "stripe")
	if rel.ToExternal != "" || len(snap.Externals) != 0 || rel.Reason != "an external dependency: this Pacto system does not deliver it" {
		t.Fatalf("edge = %+v, externals = %+v", rel, snap.Externals)
	}
}

func TestJoinedExternalNotUnresolved(t *testing.T) {
	ghost := contract.Dependency{Name: "ghost", Ref: "oci://x/ghost:1.0.0", Required: true}
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("Stripe"), ghost))
	q := NewQuery(snap)
	g, err := q.Graph(GraphQuery{Service: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Unresolved) != 1 || g.Unresolved[0].To != "ghost" {
		t.Errorf("graph unresolved = %+v", g.Unresolved)
	}
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "a", Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	if u := nb.UnresolvedDependencies.Items; len(u) != 1 || u[0].Ref != "ghost" {
		t.Errorf("neighborhood unresolved = %+v", u)
	}
	if n := q.Overview().Summary.UnresolvedRelationships; n != 1 {
		t.Errorf("unresolved relationships = %d, want 1", n)
	}
}

func TestExternalEdgeNeverIndexed(t *testing.T) {
	// A fleet service called stripe exists: the ref-less external still never
	// resolves to it, ranges against it or counts as its dependent.
	stripe := extContract("stripe", "1.0.0")
	dep := extDep("stripe")
	dep.Compatibility = "^1.0.0"
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", dep), stripe)
	rel := declaredTo(t, snap, "a", "stripe")
	if rel.ToExternal != "stripe" || rel.RangeStatus != "" || rel.ResolvedRevision != "" || rel.ToService != "" || rel.Resolved {
		t.Fatalf("edge = %+v", rel)
	}
	g, err := NewQuery(snap).Graph(GraphQuery{Service: "stripe", Direction: DirectionDependents})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 0 {
		t.Errorf("dependents of stripe = %+v %+v", g.Nodes, g.Edges)
	}
	for k, deps := range snap.reverseDeps {
		if len(deps) > 0 {
			t.Errorf("nothing depends on %s: %v", k, deps)
		}
	}
}

func TestGoldenPathNeverConsumesExternal(t *testing.T) {
	path := templated("http-service", "1.1.0")
	path.Dependencies = append(path.Dependencies, extDep("Stripe"))
	q := goldenFleet(t, ociRev(acme, path, pathFiles))
	if rec := q.snap.Externals["stripe"]; rec != nil {
		t.Fatalf("a golden path consumes nothing, got %+v", rec)
	}
}

func TestExternalSourcesPerConsumer(t *testing.T) {
	var cs []*contract.Contract
	for i := range 60 {
		cs = append(cs, extContract("a", fmt.Sprintf("1.0.%d", i), extDep("legacy-mq")))
	}
	rec := externalSnap(t, BuildOptions{}, cs...).Externals["legacy-mq"]
	if rec == nil || len(rec.Sources) != 1 || rec.SourcesTotal != 60 || rec.ConsumersTotal != 1 {
		t.Fatalf("legacy-mq = %+v", rec)
	}
	if !strings.HasSuffix(string(rec.Sources[0].Revision), "sha256:a-1.0.59") {
		t.Errorf("source revision = %q, want the newest", rec.Sources[0].Revision)
	}
}

func TestExternalEveryConsumerKeepsASource(t *testing.T) {
	var cs []*contract.Contract
	for i := range 60 {
		cs = append(cs, extContract("a", fmt.Sprintf("1.0.%d", i), extDep("legacy-mq")))
	}
	cs = append(cs, extContract("b", "1.0.0", extDep("legacy-mq")))
	rec := externalSnap(t, BuildOptions{}, cs...).Externals["legacy-mq"]
	if rec == nil || len(rec.Sources) != 2 || rec.SourcesTotal != 61 || rec.ConsumersTotal != 2 || rec.Sources[1].Service != NewServiceKey("b") {
		t.Fatalf("legacy-mq = %+v", rec)
	}
}

func TestJoinUnnameableExternalStaysUnresolved(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("日本語")))
	rel := declaredTo(t, snap, "a", "日本語")
	if rel.ToExternal != "" || len(snap.Externals) != 0 || rel.Reason != "an external dependency: this Pacto system does not deliver it" {
		t.Fatalf("edge = %+v, externals = %+v", rel, snap.Externals)
	}
}

func TestJoinNameOtherEgressNeverHitsSentinel(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("other-egress"), extDep("Other_Egress")))
	if len(snap.Externals) != 0 {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	for _, name := range []string{"other-egress", "Other_Egress"} {
		if rel := declaredTo(t, snap, "a", name); rel.ToExternal != "" || rel.Reason != "an external dependency: this Pacto system does not deliver it" {
			t.Errorf("edge = %+v", rel)
		}
	}
}

func TestCatalogLimitationMakesBuildPartial(t *testing.T) {
	lim := Limitation{Code: LimitationExternalCatalogUnavailable, Source: "external-catalog", Message: "the organization catalog could not be read"}
	snap := externalSnap(t, BuildOptions{ExternalCatalogLimitations: []Limitation{lim}}, extContract("a", "1.0.0", extDep("Stripe")))
	if snap.Completeness != CompletenessPartial || !slices.Contains(snap.Limitations, lim) {
		t.Fatalf("completeness %q, limitations %+v", snap.Completeness, snap.Limitations)
	}
	if len(snap.Sources) != 1 {
		t.Errorf("sources = %+v, want only the one configured", snap.Sources)
	}
	if plain := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("Stripe"))); plain.Completeness != CompletenessComplete {
		t.Errorf("completeness without the limitation = %q", plain.Completeness)
	}
}

func TestSnapshotRecordsCatalogRefs(t *testing.T) {
	snap := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0"))
	def := externalcatalog.DefaultRef()
	want := []ExternalCatalogRef{{Origin: def.Origin, Repository: def.Repository, Digest: def.Digest, Copy: def.Copy, Signature: def.Signature, Reason: def.Reason}}
	if !reflect.DeepEqual(snap.ExternalCatalogs, want) {
		t.Fatalf("catalogs = %+v, want %+v", snap.ExternalCatalogs, want)
	}
	defFile, _ := externalcatalog.ParseFile(externalcatalog.DefaultBytes())
	checked := def
	checked.Signature = "verified"
	again := externalSnap(t, BuildOptions{ExternalCatalog: externalcatalog.Merge(defFile, checked, nil, nil)}, extContract("a", "1.0.0"))
	if again.SnapshotID != snap.SnapshotID || again.ExternalCatalogs[0].Signature != "verified" {
		t.Errorf("a catalog's signature status moved the snapshot id: %s vs %s", again.SnapshotID, snap.SnapshotID)
	}
	org := externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Repository: "r.example/c", Digest: "sha256:org", Copy: "registry", Signature: "unknown", Signer: "s", Reason: "r"}
	f, err := externalcatalog.ParseFile([]byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/mq\n    class: legacy\n    aliases: [legacy-mq]\n"))
	if err != nil {
		t.Fatal(err)
	}
	cat := externalcatalog.Merge(defFile, def, f, &org)
	snap = externalSnap(t, BuildOptions{ExternalCatalog: cat}, extContract("a", "1.0.0", extDep("legacy-mq")))
	if len(snap.ExternalCatalogs) != 2 || snap.ExternalCatalogs[1].Digest != "sha256:org" || snap.ExternalCatalogs[1].Signer != "s" {
		t.Fatalf("catalogs = %+v", snap.ExternalCatalogs)
	}
	rec := snap.Externals["corp/mq"]
	if rec == nil || rec.Class != externalcatalog.ClassLegacy || rec.Catalog.Origin != externalcatalog.OriginOrganization || rec.Catalog.Digest != "sha256:org" {
		t.Fatalf("corp/mq = %+v", rec)
	}
}

// jsonTags lists the JSON keys of t's fields, following embedded structs.
func jsonTags(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			out = append(out, jsonTags(f.Type)...)
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

func TestSnapshotJSONKeepsFields(t *testing.T) {
	for typ, keys := range map[reflect.Type][]string{
		reflect.TypeFor[Relationship](): {"fromService", "fromRevision", "to", "toService", "resolvedRevision", "type", "provenance", "reconciliation",
			"required", "compatibility", "resolved", "requestedRef", "lockedDigest", "lockedVersion", "reason", "rangeStatus", "external", "version",
			"requires", "guaranteeStatus", "guaranteeReason", "unmet", "deprecatedInterfaces", "providerLifecycle", "hidden", "observedCount",
			"source", "observedSources", "firstSeen", "lastSeen", "toExternal"},
		reflect.TypeFor[FleetSnapshot](): {"schemaVersion", "snapshotId", "generatedAt", "services", "revisions", "targets", "relationships",
			"sources", "completeness", "limitations", "uncontractedCallers", "provisioning", "usage", "externals", "externalCatalogs"},
	} {
		have := jsonTags(typ)
		for _, k := range keys {
			if !slices.Contains(have, k) {
				t.Errorf("%s lost JSON key %q", typ.Name(), k)
			}
		}
	}
	b, err := json.Marshal(Relationship{})
	if err != nil || strings.Contains(string(b), "toExternal") {
		t.Errorf("toExternal must be omitted when empty: %s", b)
	}
}

// TestExternalTwoPositionsOneConsumer: one consumer naming the same external
// twice keeps one source per position, in position order.
func TestExternalTwoPositionsOneConsumer(t *testing.T) {
	rec := externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", extDep("stripe-api"), extDep("legacy-mq"), extDep("Stripe"))).Externals["stripe"]
	if rec == nil || len(rec.Sources) != 2 || rec.Sources[0].Index != 0 || rec.Sources[1].Index != 2 || rec.SourcesTotal != 2 || rec.ConsumersTotal != 1 {
		t.Fatalf("stripe = %+v", rec)
	}
}

// TestJoinIdentityOverridesAlias: a declared identity (pactoVersion 2.5) wins
// over the name, so a dependency the catalog does not know by name joins the
// node it names; one that does not parse falls back to the name.
func TestJoinIdentityOverridesAlias(t *testing.T) {
	named := extDep("payments-provider")
	named.Identity = "stripe"
	broken := extDep("legacy-mq")
	broken.Identity = "Not An Identity"
	snap := externalSnap(t, BuildOptions{}, extContract("checkout", "1.0.0", named, broken))
	rec := snap.Externals["stripe"]
	if rec == nil || len(rec.Sources) != 1 || rec.Sources[0].Match != externalcatalog.MatchIdentity || rec.Label != "Stripe" {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	rel := declaredTo(t, snap, "checkout", "payments-provider")
	if rel.ToExternal != "stripe" || rel.Reason != "joined external stripe through identity; this Pacto system does not deliver it" {
		t.Errorf("edge = %+v", rel)
	}
	if mq := snap.Externals["legacy-mq"]; mq == nil || mq.Sources[0].Match != externalcatalog.MatchNormalizedName {
		t.Errorf("legacy-mq = %+v", mq)
	}
}
