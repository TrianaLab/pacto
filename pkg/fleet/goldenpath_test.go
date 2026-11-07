package fleet

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// pgPin pins postgresql 16.1.0, the reference a configuration resolves through.
var pgPin = "oci://ghcr.io/acme/postgresql@" + testDigest(acme, "postgresql", "16.1.0")

// pathFiles is the bundle of the http-service golden path.
var pathFiles = fstest.MapFS{"README.md": {Data: []byte("# http-service\n")}, "interfaces/openapi.yaml": {Data: []byte("openapi: 3.1.0\n")}}

// pathContentHash is the content digest of pathFiles, the digest a created
// service records in service.origin.
func pathContentHash(t *testing.T) string {
	t.Helper()
	d, err := lock.HashFS(pathFiles)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// usesPostgres is a 2.7 contract that depends on postgresql and takes its db
// configuration from it.
func usesPostgres(name, version string) *contract.Contract {
	c := &contract.Contract{PactoVersion: "2.7", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: name + "-team"}, Domain: "commerce"}}
	c.Dependencies = []contract.Dependency{{Name: "postgresql", Ref: "oci://ghcr.io/acme/postgresql", Required: true, Compatibility: "^16.0.0"}}
	c.Configurations = []contract.Configuration{{Name: "db", Ref: pgPin}}
	return c
}

// templated is usesPostgres declaring service.template.
func templated(name, version string) *contract.Contract {
	c := usesPostgres(name, version)
	c.Service.Owner = contract.Owner{Team: "platform"}
	c.Service.Template = &contract.Template{}
	return c
}

// postgres is postgresql 16.1.0, which provides the postgresql offering.
func postgres() RawRevision {
	pg := svc25("postgresql", "16.1.0", "postgresql")
	pg.Configurations = []contract.Configuration{{Name: "provisioning", Schema: "schema.json"}}
	return ociRev(acme, pg, fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object"}`)}})
}

// createdFrom is gift-cards 0.1.0, created from the golden path digest.
func createdFrom(digest string) RawRevision {
	c := &contract.Contract{PactoVersion: "2.7", Service: contract.Service{Name: "gift-cards", Version: "0.1.0", Owner: contract.Owner{Team: "checkout"},
		Origin: &contract.Origin{Ref: "oci://ghcr.io/acme/http-service:1.0.0", Digest: digest}}}
	return ociRev(acme, c, nil)
}

// goldenFleet holds the http-service golden path, gift-cards created from it,
// postgresql and orders, an ordinary consumer of postgresql. extra joins it.
func goldenFleet(t *testing.T, extra ...RawRevision) *Query {
	t.Helper()
	revs := append([]RawRevision{
		ociRev(acme, templated("http-service", "1.0.0"), pathFiles),
		createdFrom(pathContentHash(t)),
		postgres(),
		ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}, extra...)
	return buildDomainSnap(t, "oci", &Collection{Revisions: revs})
}

func TestDeriveTemplate(t *testing.T) {
	q := goldenFleet(t,
		// an older template revision does not make a service a path
		ociRev(acme, templated("was-path", "1.0.0"), nil), ociRev(acme, usesPostgres("was-path", "2.0.0"), nil),
		// the newest readable revision decides
		ociRev(acme, usesPostgres("became-path", "1.0.0"), nil), ociRev(acme, templated("became-path", "2.0.0"), nil))
	for name, want := range map[string]bool{"http-service": true, "became-path": true, "was-path": false, "orders": false, "gift-cards": false} {
		if got := acmeService(q, name).Template; got != want {
			t.Errorf("%s template = %v, want %v", name, got, want)
		}
	}
	if acmeService(q, "http-service").Status != StatusNotEvaluated {
		t.Errorf("a golden path keeps the service status: %s", acmeService(q, "http-service").Status)
	}
}

func TestDeriveTemplate_UnknownPlacementKeepsAService(t *testing.T) {
	for name, kube := range map[string]Source{
		"unavailable": NewFailingSource("k8s", SourceKindKubernetes, errors.New("down")),
		"partial":     NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}}),
	} {
		t.Run(name, func(t *testing.T) {
			snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
				NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, templated("http-service", "1.0.0"), pathFiles), postgres()}}), kube)
			if err != nil {
				t.Fatal(err)
			}
			path, pg := NewServiceKeyDomain(acme, "http-service"), NewServiceKeyDomain(acme, "postgresql")
			if snap.Services[path].Template || len(snap.templateEdges) != 0 || !slices.Contains(snap.reverseDeps[pg], path) {
				t.Errorf("template %v, template edges %d, postgresql dependents %v", snap.Services[path].Template, len(snap.templateEdges), snap.reverseDeps[pg])
			}
		})
	}
}

func TestResolveOrigin(t *testing.T) {
	byDigest := ociRev(acme, templated("http-service", "1.0.0"), pathFiles).Digest
	twin := ociRev(acme, templated("twin-path", "1.0.0"), pathFiles)
	plain := ociRev(acme, usesPostgres("plain", "1.0.0"), nil)
	tests := []struct {
		name, digest string
		extra        []RawRevision
		want         ServiceKey
		reason       string
	}{
		{"by content hash", pathContentHash(t), nil, NewServiceKeyDomain(acme, "http-service"), ""},
		{"by digest", byDigest, nil, NewServiceKeyDomain(acme, "http-service"), ""},
		{"no match", "sha256:" + strings.Repeat("0", 64), nil, "", "no golden path revision in this snapshot has digest sha256:000"},
		{"two paths with one digest", pathContentHash(t), []RawRevision{twin}, "", "the digest matches more than one golden path"},
		{"a revision that is no golden path", plain.Digest, []RawRevision{plain}, "", "no golden path revision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := buildDomainSnap(t, "oci", &Collection{Revisions: append([]RawRevision{
				ociRev(acme, templated("http-service", "1.0.0"), pathFiles), createdFrom(tt.digest),
			}, tt.extra...)})
			o := acmeService(q, "gift-cards").Origin
			if o == nil || o.GoldenPath != tt.want || !strings.HasPrefix(o.Reason, tt.reason) || o.Digest != tt.digest || o.Ref != "oci://ghcr.io/acme/http-service:1.0.0" {
				t.Fatalf("origin = %+v, want golden path %q, reason %q", o, tt.want, tt.reason)
			}
			if tt.want != "" && q.snap.Revisions[o.Revision].ServiceKey != tt.want {
				t.Errorf("origin revision = %s", o.Revision)
			}
		})
	}
	if acmeService(goldenFleet(t), "orders").Origin != nil {
		t.Error("a service that declares no origin has none")
	}
}

func TestTemplateEdgesAreNotConsumption(t *testing.T) {
	q := goldenFleet(t)
	path, pg := NewServiceKeyDomain(acme, "http-service"), NewServiceKeyDomain(acme, "postgresql")
	if got := q.snap.reverseDeps[pg]; slices.Contains(got, path) || !slices.Contains(got, NewServiceKeyDomain(acme, "orders")) {
		t.Errorf("postgresql dependents = %v", got)
	}
	if q.snap.forwardDeps[path] != nil {
		t.Errorf("golden path forward deps = %v", q.snap.forwardDeps[path])
	}
	for _, rel := range q.snap.Relationships {
		if rel.FromService == path {
			t.Errorf("golden path edge in Relationships: %+v", rel)
		}
	}
	if len(q.snap.templateEdges) != 2 {
		t.Errorf("template edges = %+v", q.snap.templateEdges)
	}
	if got := entityKeys(t, q, EntityFilter{Consumes: "postgresql"}); !slices.Equal(got, []string{"orders"}) {
		t.Errorf("consumers of postgresql = %v", got)
	}
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Offering.Consumers.Items {
		if u.Consumer.Key == string(path) {
			t.Errorf("offering consumer %+v", u)
		}
	}
	al, _ := q.Attention(AttentionFilter{Limit: MaxAttentionLimit})
	for _, it := range al.Items {
		if it.Service == string(path) {
			t.Errorf("attention about the golden path: %+v", it)
		}
	}
}

// The golden path's own pages still read the edges it declares.
func TestTemplateEdgesResolveForThePath(t *testing.T) {
	q := goldenFleet(t)
	pathRev := acmeService(q, "http-service").Revisions[0]
	if configRefTarget(q.snap, pathRev, "db") == "" {
		t.Error("the golden path's configuration ref no longer resolves")
	}
	rd, err := q.EntityDetail(KindRevision, string(pathRev))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := rd.Revision.Configurations.Items; len(cfg) != 1 || cfg[0].Resolution == nil || !cfg[0].Resolution.Resolved {
		t.Errorf("golden path revision configurations = %+v", cfg)
	}
	if deps := rd.Revision.Dependencies.Items; len(deps) != 2 {
		t.Errorf("golden path revision dependencies = %+v", deps)
	}
}

// Edges route by the service: a path whose older revision was ordinary keeps
// every edge out, a service whose older revision was a path keeps every edge in.
func TestTemplateEdgesRouteByService(t *testing.T) {
	q := goldenFleet(t,
		ociRev(acme, usesPostgres("became-path", "1.0.0"), nil), ociRev(acme, templated("became-path", "2.0.0"), nil),
		ociRev(acme, templated("was-path", "1.0.0"), nil), ociRev(acme, usesPostgres("was-path", "2.0.0"), nil))
	count := func(rels []Relationship, name string) int {
		n := 0
		for _, rel := range rels {
			n += b2i(rel.FromService == NewServiceKeyDomain(acme, name))
		}
		return n
	}
	if count(q.snap.Relationships, "became-path") != 0 || count(q.snap.templateEdges, "became-path") != 4 {
		t.Errorf("became-path edges: %d in Relationships, %d template", count(q.snap.Relationships, "became-path"), count(q.snap.templateEdges, "became-path"))
	}
	if count(q.snap.Relationships, "was-path") != 4 || count(q.snap.templateEdges, "was-path") != 0 {
		t.Errorf("was-path edges: %d in Relationships, %d template", count(q.snap.Relationships, "was-path"), count(q.snap.templateEdges, "was-path"))
	}
}

// deployedTemplateFleet runs http-service, which declares service.template,
// in two targets.
func deployedTemplateFleet(t *testing.T) *Query {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, templated("http-service", "1.0.0"), pathFiles), postgres()}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
			{Scope: "prod", Kind: SourceKindKubernetes, Name: "http-a", Service: "http-service", Domain: acme, Compliance: StatusCompliant},
			{Scope: "dev", Kind: SourceKindKubernetes, Name: "http-b", Service: "http-service", Domain: acme, Compliance: StatusCompliant},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestDeployedTemplateStaysAService(t *testing.T) {
	q := deployedTemplateFleet(t)
	snap := q.snap
	path := NewServiceKeyDomain(acme, "http-service")
	if snap.Services[path].Template || !slices.Contains(snap.reverseDeps[NewServiceKeyDomain(acme, "postgresql")], path) {
		t.Fatalf("template %v, dependents %v", snap.Services[path].Template, snap.reverseDeps)
	}
	if ov := q.Overview(); ov.Summary.Services != 2 || ov.Summary.GoldenPaths != 0 {
		t.Errorf("overview services %d, golden paths %d", ov.Summary.Services, ov.Summary.GoldenPaths)
	}
	al, _ := q.Attention(AttentionFilter{Category: categoryGoldenPath})
	if al.Total != 1 || al.Items[0].Code != "GOLDEN_PATH_DEPLOYED" || al.Items[0].Severity != severityWarning ||
		al.Items[0].Reason != "http-service declares service.template but runs in 2 operational targets; it is treated as a service" {
		t.Errorf("attention = %+v", al.Items)
	}
	one := NewQuery(snap)
	one.snap.Services[path].Targets = one.snap.Services[path].Targets[:1]
	if items := one.deployedTemplateAttention(); len(items) != 1 || !strings.Contains(items[0].Reason, "runs in 1 operational target;") {
		t.Errorf("one target: %+v", items)
	}
}

func TestGoldenPathExcludedFromOverview(t *testing.T) {
	bad := mustParse(t, invalidYAML)
	bad.PactoVersion, bad.Service.Name, bad.Service.Version, bad.Service.Owner = "2.7", "bad-path", "2.0.0", contract.Owner{Team: "platform"}
	bad.Service.Template = &contract.Template{}
	invalidNewer := RawRevision{Bundle: &contract.Bundle{Contract: bad, RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}, Domain: acme, Digest: "sha256:bad"}
	q := goldenFleet(t, ociRev(acme, templated("bad-path", "1.0.0"), nil), invalidNewer)
	if !acmeService(q, "bad-path").Template || acmeService(q, "bad-path").Status != StatusInvalid {
		t.Fatalf("bad-path: template %v, status %s", acmeService(q, "bad-path").Template, acmeService(q, "bad-path").Status)
	}
	sum := q.Overview().Summary
	// gift-cards, postgresql and orders; four revisions; no invalid one.
	if sum.Services != 3 || sum.Revisions != 3 || sum.InvalidRevisions != 0 || sum.GoldenPaths != 2 || sum.InvalidGoldenPaths != 1 {
		t.Errorf("services %d, revisions %d, invalid %d, golden paths %d, invalid golden paths %d",
			sum.Services, sum.Revisions, sum.InvalidRevisions, sum.GoldenPaths, sum.InvalidGoldenPaths)
	}
	if sum.Ownership.Total() != 3 || sum.Readiness.Total() != 3 || sum.ServicesNeedingAttention != 0 {
		t.Errorf("ownership %+v, readiness %+v, needing attention %d", sum.Ownership, sum.Readiness, sum.ServicesNeedingAttention)
	}
	domains := 0
	for _, d := range sum.ByDomain {
		domains += d.Services
		if d.Domain == "commerce" && d.Services != 1 {
			t.Errorf("commerce domain = %+v, want orders only", d)
		}
	}
	if domains != 3 {
		t.Errorf("domain rows count %d services", domains)
	}
}

func TestGoldenPathExcludedFromOwnerAndDomain(t *testing.T) {
	q := goldenFleet(t)
	owner, err := q.EntityDetail(KindOwner, "team:platform")
	if err != nil {
		t.Fatal(err)
	}
	if o := owner.Owner; o.Summary.Services != 0 || o.Summary.Revisions != 0 || o.Services.Total != 0 || o.Attention.Total != 0 {
		t.Errorf("platform owner = %+v", o.Summary)
	}
	dom, err := q.EntityDetail(KindDomain, "commerce")
	if err != nil {
		t.Fatal(err)
	}
	if d := dom.Domain; d.Summary.Services != 1 || d.Summary.Revisions != 1 || d.Services.Total != 1 {
		t.Errorf("commerce domain = %+v", d.Summary)
	}
	pathOnly := goldenFleet(t)
	pathOnly.snap.Services[NewServiceKeyDomain(acme, "orders")].BusinessDomain = ""
	if _, err := pathOnly.EntityDetail(KindDomain, "commerce"); err != nil {
		t.Errorf("a domain holding only a golden path keeps its page: %v", err)
	}
	rows := map[string]int{}
	for _, ref := range append(listRefs(t, q, KindOwner), listRefs(t, q, KindDomain)...) {
		rows[string(ref.Kind)+"/"+ref.Key] = ref.Services
	}
	if n, ok := rows["owner/team:platform"]; !ok || n != 0 || rows["domain/commerce"] != 1 {
		t.Errorf("list rows = %v", rows)
	}
}

// listRefs is the entity list of one kind.
func listRefs(t *testing.T, q *Query, kind EntityKind) []EntityRef {
	t.Helper()
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{kind}, Limit: MaxEntityLimit})
	if err != nil {
		t.Fatal(err)
	}
	return l.Entities
}

func TestGoldenPathHasNoOffering(t *testing.T) {
	path := templated("http-service", "1.0.0")
	path.Service.Offering = "postgresql"
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{ociRev(acme, path, pathFiles), postgres()}})
	s := acmeService(q, "http-service")
	if s.Offering != "" || s.OfferingProvenance != "" {
		t.Errorf("golden path offering = %q (%s)", s.Offering, s.OfferingProvenance)
	}
	if o, p, c := offeringOf(q.snap, s); o != "" || p != "" || c != nil {
		t.Errorf("offeringOf = %q, %q, %v", o, p, c)
	}
	if hasLimitation(q.snap.Limitations, LimitationOfferingConflict) {
		t.Error("a golden path raised OFFERING_CONFLICT")
	}
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if d.Offering.Providers.Total != 1 || d.Offering.Providers.Items[0].Service.Label != "postgresql" {
		t.Errorf("providers = %+v", d.Offering.Providers.Items)
	}
}
