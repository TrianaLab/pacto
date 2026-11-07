package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// createdNamed is a service created from the golden path digest.
func createdNamed(name, digest string) RawRevision {
	r := createdFrom(digest)
	r.Bundle.Contract.Service.Name = name
	r.RequestedRef, r.Digest = "oci://"+acme+"/"+name+":0.1.0", testDigest(acme, name, "0.1.0")
	return r
}

// goldenRestrictFleet holds the registry-only golden path (path, whose older
// revision 0.9.0 is ordinary), gift-cards and postgresql running in the hush
// namespace, and promo-cards, also created from the path, running in shop.
// extra adds sources.
func goldenRestrictFleet(t *testing.T, path *contract.Contract, extra ...Source) *Query {
	t.Helper()
	k := SourceKindKubernetes
	digest := pathContentHash(t)
	reg := NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{
		ociRev(acme, path, pathFiles), ociRev(acme, usesPostgres(path.Service.Name, "0.9.0"), nil),
		createdFrom(digest), createdNamed("promo-cards", digest), postgres(),
	}})
	kube := NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{
		{Scope: "hush", Kind: k, Name: "gift-cards", Service: "gift-cards", Domain: acme, Compliance: StatusCompliant},
		{Scope: "hush", Kind: k, Name: "postgresql", Service: "postgresql", Domain: acme, Compliance: StatusCompliant},
		{Scope: "shop", Kind: k, Name: "promo-cards", Service: "promo-cards", Domain: acme, Compliance: StatusCompliant},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, append([]Source{reg, kube}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// hiddenPathFleet is goldenRestrictFleet with the golden path given a target
// in hush after classification, so a restricted view hides a path that
// promo-cards' origin resolves to.
func hiddenPathFleet(t *testing.T) *Query {
	t.Helper()
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	path := q.snap.Services[NewServiceKeyDomain(acme, "http-service")]
	tk := TargetKey("hush/kubernetes/http-service")
	q.snap.Targets[tk] = &TargetRecord{Key: tk, Scope: "hush", Kind: SourceKindKubernetes, Name: "http-service", ServiceKey: path.Key}
	path.Targets = []TargetKey{tk}
	return q
}

func TestRestrictGoldenPathVisible(t *testing.T) {
	r := goldenRestrictFleet(t, templated("http-service", "1.0.0")).Restrict(shopOnly)
	path := r.snap.Services[NewServiceKeyDomain(acme, "http-service")]
	if path == nil || !path.Template {
		t.Fatalf("golden path = %+v", path)
	}
	down := goldenRestrictFleet(t, templated("http-service", "1.0.0"), NewFailingSource("k8s-2", SourceKindKubernetes, errors.New("down"))).Restrict(shopOnly)
	if down.snap.Services[NewServiceKeyDomain(acme, "http-service")] != nil {
		t.Error("with a Kubernetes source down the path cannot be placed and stays hidden")
	}
}

func TestRestrictGoldenPathRevisions(t *testing.T) {
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	r := q.Restrict(shopOnly)
	key := NewServiceKeyDomain(acme, "http-service")
	if len(q.snap.Services[key].Revisions) != 2 {
		t.Fatalf("unrestricted revisions = %v", q.snap.Services[key].Revisions)
	}
	revs := r.snap.Services[key].Revisions
	if len(revs) != 1 || r.snap.Revisions[revs[0]].Version != "1.0.0" {
		t.Errorf("restricted revisions = %v", revs)
	}
	for _, rv := range r.snap.Revisions {
		if rv.ServiceKey == key && rv.Version == "0.9.0" {
			t.Error("the older ordinary revision is in the view")
		}
	}
	if len(r.snap.forwardDepsByRevision) != 0 && r.snap.forwardDepsByRevision[revs[0]] != nil {
		t.Error("a golden path revision has forward dependencies")
	}
}

func TestRestrictGoldenPathHiddenCreated(t *testing.T) {
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	r := q.Restrict(shopOnly)
	key := NewServiceKeyDomain(acme, "http-service")
	if got := r.snap.Services[key].HiddenCreated; got != 1 {
		t.Errorf("hidden created = %d", got)
	}
	created := func(q *Query) int {
		for _, ref := range listRefs(t, q, KindService) {
			if ref.Key == string(key) {
				return ref.Created
			}
		}
		return -1
	}
	if created(q) != 2 || created(r) != 1 {
		t.Errorf("created: unrestricted %d, restricted %d", created(q), created(r))
	}
	l, err := r.Entities(EntityFilter{CreatedFrom: string(key)})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []any{r.SnapshotForSerialization(), l} {
		data, _ := json.Marshal(body)
		if strings.Contains(string(data), "gift-cards") {
			t.Error("the restricted answer names the hidden created service")
		}
	}
	if l.Total != 1 || l.Entities[0].Label != "promo-cards" {
		t.Errorf("created from the path = %+v", l.Entities)
	}
}

func TestRestrictGoldenPathHasNoOffering(t *testing.T) {
	path := templated("http-service", "1.0.0")
	path.Service.Offering = "postgresql"
	r := goldenRestrictFleet(t, path).Restrict(shopOnly)
	s := r.snap.Services[NewServiceKeyDomain(acme, "http-service")]
	if s.HiddenCreated != 1 || s.Offering != "" || s.OfferingProvenance != "" {
		t.Errorf("golden path in the view = %+v", s)
	}
	if hasLimitation(r.snap.Limitations, LimitationOfferingConflict) {
		t.Error("the view raised OFFERING_CONFLICT for a golden path")
	}
}

func TestRestrictOriginBlanked(t *testing.T) {
	r := hiddenPathFleet(t).Restrict(shopOnly)
	if r.snap.Services[NewServiceKeyDomain(acme, "http-service")] != nil {
		t.Fatal("the path is visible")
	}
	o := r.snap.Services[NewServiceKeyDomain(acme, "promo-cards")].Origin
	if o == nil || o.GoldenPath != "" || o.Revision != "" || o.Reason != "the golden path it names is not in this view" ||
		o.Ref != "oci://ghcr.io/acme/http-service:1.0.0" || o.Digest != pathContentHash(t) {
		t.Errorf("origin = %+v", o)
	}
}

func TestRestrictOriginDoesNotMutateSnapshot(t *testing.T) {
	hidden, visible := hiddenPathFleet(t), goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	var wg sync.WaitGroup
	for range 4 {
		for _, q := range []*Query{hidden, visible} {
			wg.Go(func() { q.Restrict(shopOnly) })
		}
	}
	wg.Wait()
	path := NewServiceKeyDomain(acme, "http-service")
	if o := hidden.snap.Services[NewServiceKeyDomain(acme, "promo-cards")].Origin; o.GoldenPath != path || o.Reason != "" {
		t.Errorf("source origin = %+v", o)
	}
	if n := visible.snap.Services[path].HiddenCreated; n != 0 || len(visible.snap.Services[path].Revisions) != 2 {
		t.Errorf("source golden path: hidden created %d, revisions %v", n, visible.snap.Services[path].Revisions)
	}
}

func TestRestrictTemplateEdges(t *testing.T) {
	q := goldenRestrictFleet(t, templated("http-service", "1.0.0"))
	r := q.Restrict(shopOnly)
	path := r.snap.Services[NewServiceKeyDomain(acme, "http-service")]
	if len(q.snap.templateEdges) != 4 || len(r.snap.templateEdges) != 2 {
		t.Fatalf("template edges: unrestricted %d, restricted %+v", len(q.snap.templateEdges), r.snap.templateEdges)
	}
	for _, rel := range r.snap.templateEdges {
		if rel.FromRevision != path.Revisions[0] || !rel.Hidden || rel.ToService != "" {
			t.Errorf("restricted template edge = %+v", rel)
		}
	}
	for _, rel := range r.snap.Relationships {
		if rel.FromService == path.Key {
			t.Errorf("golden path edge in Relationships: %+v", rel)
		}
	}
	if path.HiddenDependencies != 0 || path.HiddenDependents != 0 {
		t.Errorf("a golden path counts hidden edges: %+v", path)
	}
	rev := r.snap.Revisions[path.Revisions[0]]
	if rev == q.snap.Revisions[path.Revisions[0]] {
		t.Error("the path revision whose configuration resolves to a hidden provider was not revalidated")
	}
}
