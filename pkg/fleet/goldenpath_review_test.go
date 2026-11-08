package fleet

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestDeriveTemplate_TargetSourceDownKeepsAService pins that any source that
// reports operational targets being down keeps a target-less service that
// declares service.template a service: it may run where that source reports.
func TestDeriveTemplate_TargetSourceDownKeepsAService(t *testing.T) {
	for _, kind := range []string{SourceKindEvidenceHTTP, SourceKindTargetState} {
		t.Run(kind, func(t *testing.T) {
			snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
				NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, templated("http-service", "1.0.0"), pathFiles), postgres()}}),
				NewMemorySource("k8s", SourceKindKubernetes, &Collection{}),
				NewFailingSource("ext", kind, errors.New("down")))
			if err != nil {
				t.Fatal(err)
			}
			path, pg := NewServiceKeyDomain(acme, "http-service"), NewServiceKeyDomain(acme, "postgresql")
			if snap.Services[path].Template || !slices.Contains(snap.reverseDeps[pg], path) {
				t.Errorf("template %v, postgresql dependents %v", snap.Services[path].Template, snap.reverseDeps[pg])
			}
		})
	}
}

// TestCreatedCountLeavesOutDerivedPaths pins that a golden path derived from
// another is not counted among the services created from it, since the
// Services list the count opens never lists a golden path.
func TestCreatedCountLeavesOutDerivedPaths(t *testing.T) {
	derived := templated("worker-path", "1.0.0")
	derived.Service.Origin = &contract.Origin{Ref: "oci://ghcr.io/acme/http-service:1.0.0", Digest: pathContentHash(t)}
	q := goldenFleet(t, ociRev(acme, derived, fstest.MapFS{"x": {Data: []byte("x")}}))
	key := NewServiceKeyDomain(acme, "http-service")
	if !acmeService(q, "worker-path").Template || acmeService(q, "worker-path").Origin.GoldenPath != key {
		t.Fatalf("worker-path = %+v", acmeService(q, "worker-path"))
	}
	for _, ref := range listRefs(t, q, KindService) {
		if ref.Key == string(key) && ref.Created != 1 {
			t.Errorf("created count = %d, want 1", ref.Created)
		}
	}
	if d := q.templateDetail(q.snap.Services[key]); d.CreatedServices.Total != 1 || d.CreatedServices.Items[0].Label != "gift-cards" {
		t.Errorf("created services = %+v", d.CreatedServices)
	}
}

// TestTemplateDetailCountsHiddenRequirements pins that a requirement on a
// provider the view hides is counted, never named.
func TestTemplateDetailCountsHiddenRequirements(t *testing.T) {
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	key := NewServiceKeyDomain(acme, "http-service")
	if d := q.templateDetail(q.snap.Services[key]); d.HiddenRequirements != 0 || d.Requirements.Total == 0 {
		t.Errorf("unrestricted: %+v", d)
	}
	r := q.Restrict(shopOnly)
	if d := r.templateDetail(r.snap.Services[key]); d.HiddenRequirements != 1 || d.Requirements.Total != 0 {
		t.Errorf("restricted: hidden %d, requirements %+v", d.HiddenRequirements, d.Requirements)
	}
}

// TestRestrictDroppedRevisionIsHidden pins that the older ordinary revision a
// golden path visible only as one drops is hidden from everything that
// pointed at it: a visible consumer's reference no longer resolves to it and
// is validated again without it.
func TestRestrictDroppedRevisionIsHidden(t *testing.T) {
	old := testDigest(acme, "http-service", "0.9.0")
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "promo-svc", Version: "1.0.0", Owner: contract.Owner{Team: "shop"}}}
	c.Configurations = []contract.Configuration{{Name: "db", Ref: "oci://ghcr.io/acme/http-service@" + old}}
	k := SourceKindKubernetes
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"),
		NewMemorySource("oci-2", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, c, nil)}}),
		NewMemorySource("k8s-2", k, &Collection{Targets: []RawTarget{{Scope: "shop", Kind: k, Name: "promo-svc", Service: "promo-svc", Domain: acme, Compliance: StatusCompliant}}}))
	consumer := NewServiceKeyDomain(acme, "promo-svc")
	edge := func(q *Query) Relationship {
		for _, rel := range q.snap.Relationships {
			if rel.FromService == consumer && rel.Type == RelationshipConfigRef {
				return rel
			}
		}
		t.Fatal("no configuration edge")
		return Relationship{}
	}
	dropped := edge(q).ResolvedRevision
	if q.snap.Revisions[dropped] == nil || q.snap.Revisions[dropped].Version != "0.9.0" {
		t.Fatalf("unrestricted edge = %+v", edge(q))
	}
	r := q.Restrict(shopOnly)
	if r.snap.Revisions[dropped] != nil || edge(r).ResolvedRevision != "" {
		t.Errorf("restricted edge = %+v", edge(r))
	}
	rev := q.snap.Services[consumer].Revisions[0]
	if r.snap.Revisions[rev] == q.snap.Revisions[rev] {
		t.Error("the consumer revision whose reference resolved to the dropped revision was not validated again")
	}
}

// TestRestrictDroppedRevisionLimitationIsHidden pins that a limitation naming
// the older revision a golden path visible only as one drops is left out of
// the restricted view, like one naming a hidden service.
func TestRestrictDroppedRevisionLimitationIsHidden(t *testing.T) {
	path := NewServiceKeyDomain(acme, "http-service")
	var dropped RevisionKey
	base := goldenRestrictFleet(t, templated("http-service", "1.0.0")).snap
	for _, k := range base.Services[path].Revisions {
		if base.Revisions[k].Version == "0.9.0" {
			dropped = k
		}
	}
	msg := "bundle " + string(dropped) + " could not be read"
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"),
		NewMemorySource("oci-2", "oci", &Collection{Limitations: []Limitation{{Code: "bundle-unreadable", Message: msg}}}))
	has := func(q *Query) bool {
		return slices.ContainsFunc(q.snap.Limitations, func(l Limitation) bool { return l.Message == msg })
	}
	r := q.Restrict(shopOnly)
	if r.snap.Revisions[dropped] != nil || !has(q) || has(r) {
		t.Errorf("dropped %q kept %v, unrestricted has %v, restricted has %v", dropped, r.snap.Revisions[dropped] != nil, has(q), has(r))
	}
}
