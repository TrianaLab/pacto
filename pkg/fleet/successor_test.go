package fleet

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// asPGProvider makes rev a postgresql offering provider.
func asPGProvider(rev RawRevision) RawRevision {
	rev.Bundle.Contract.Service.Offering = "postgresql"
	return rev
}

// lockedTo16 pins rev's pg dependency to 16.1.0, so it reaches the deprecated
// revision whatever its range accepts.
func lockedTo16(rev RawRevision) RawRevision {
	rev.Lock = &lock.Lock{Dependencies: []lock.Entry{{Name: "pg", Digest: "sha256:pg16.1.0", Version: "16.1.0"}}}
	return rev
}

// successorFleet: pg 16.0.0 declares nothing, 16.1.0 is deprecated for l, and
// extra holds any other provider revision; app16 accepts ^16, wide accepts
// >=16 <18 and any accepts every version, both locked to 16.1.0.
func successorFleet(t *testing.T, l *contract.InterfaceLifecycle, extra ...RawRevision) *Query {
	t.Helper()
	return successorFleetRunning(t, "", l, extra...)
}

// successorFleetRunning is successorFleet with a target running pg at
// running, none when it is empty.
func successorFleetRunning(t *testing.T, running string, l *contract.InterfaceLifecycle, extra ...RawRevision) *Query {
	t.Helper()
	revs := []RawRevision{
		asPGProvider(lcRev("pg", "16.0.0", nil)),
		asPGProvider(lcRev("pg", "16.1.0", l)),
		lcRev("app16", "1.0.0", nil, pgDep("^16.0.0")),
		lockedTo16(lcRev("wide", "1.0.0", nil, pgDep(">=16.0.0 <18.0.0"))),
		lockedTo16(lcRev("any", "1.0.0", nil, pgDep(""))),
	}
	for _, r := range extra {
		revs = append(revs, asPGProvider(r))
	}
	col := &Collection{Revisions: revs}
	if running != "" {
		col.Targets = []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pg", Service: "pg", Digest: "sha256:pg" + running}}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func succDetail(t *testing.T, q *Query) (*OfferingDetailData, ProductMeta) {
	t.Helper()
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	return d.Offering, d.Meta
}

func useOf(t *testing.T, d *OfferingDetailData, consumer string) OfferingUse {
	t.Helper()
	for _, u := range d.Consumers.Items {
		if u.Consumer.Key == consumer {
			return u
		}
	}
	t.Fatalf("no use from %s", consumer)
	return OfferingUse{}
}

func hasLimitationCode(m ProductMeta, code string) bool {
	return hasLimitation(m.Limitations, code)
}

func TestSuccessorResolvesByNameAndVersion(t *testing.T) {
	q := lifecycleRangeFleet(t)
	info := q.resolveSuccessor(q.snap.Services["pg"], deprecated16)
	if info == nil || info.Provenance != ProvenanceInferred || info.Revision == nil || info.Revision.Version != "17.0.0" || info.Reason != "" || info.Ref != deprecated16.Successor {
		t.Errorf("successor = %+v", info)
	}
	if q.resolveSuccessor(q.snap.Services["pg"], nil) != nil || q.resolveSuccessor(q.snap.Services["pg"], &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}) != nil {
		t.Error("no successor declared gives no successor")
	}
}

func TestSuccessorResolvesDigest(t *testing.T) {
	q := lifecycleRangeFleet(t)
	info := q.resolveSuccessor(q.snap.Services["pg"], &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "oci://ghcr.io/acme/pg@sha256:pg17.0.0"})
	if info == nil || info.Revision == nil || info.Revision.Version != "17.0.0" {
		t.Errorf("successor = %+v", info)
	}
}

func TestSuccessorUnresolvedIsUnknown(t *testing.T) {
	q := successorFleet(t, deprecated16)
	d, meta := succDetail(t, q)
	s := d.Providers.Items[0].Successor
	if s == nil || s.Provenance != provenanceUnknown || s.Revision != nil || !strings.Contains(s.Reason, "no revision of pg") {
		t.Fatalf("successor = %+v", s)
	}
	if !hasLimitationCode(meta, LimitationSuccessorUnresolved) {
		t.Errorf("limitations = %+v", meta.Limitations)
	}
	if got := useOf(t, d, "app16").SuccessorRange; got != RangeUnknown {
		t.Errorf("an unresolved successor is never satisfied, got %q", got)
	}
	for _, it := range lifecycleItems(t, q) {
		if it.Code != "DEPENDENCY_SERVICE_DEPRECATED" {
			t.Errorf("an unresolved successor raises nothing extra: %+v", it)
		}
	}
}

func TestSuccessorOtherDomainUnresolved(t *testing.T) {
	other := lcRev("pgnext", "1.0.0", nil)
	other.Domain = "ghcr.io/other"
	q := successorFleet(t, deprecated16, other)
	s := q.resolveSuccessor(q.snap.Services["pg"], &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "oci://ghcr.io/other/pgnext:1.0.0"})
	if s == nil || s.Revision != nil || !strings.Contains(s.Reason, "no service pgnext in pg's registry domain") {
		t.Errorf("a successor in another domain resolves to nothing, got %+v", s)
	}
}

func TestSuccessorNotURLUnknown(t *testing.T) {
	twin := lcRev("pg", "17.0.0", nil)
	twin.Digest = "sha256:twin"
	q := successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil), twin)
	for _, tc := range []struct{ ref, reason string }{
		{"pg-17", "not an oci:// reference"},
		{"https://example.com/pg:17.0.0", "not an oci:// reference"},
		{"oci://ghcr.io/acme/pg", "names no version or digest"},
		{"oci://ghcr.io/acme/pg:17.0.0", "2 revisions of pg match it"},
	} {
		s := q.resolveSuccessor(q.snap.Services["pg"], &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: tc.ref})
		if s == nil || s.Provenance != provenanceUnknown || s.Revision != nil || !strings.Contains(s.Reason, tc.reason) {
			t.Errorf("%s: successor = %+v, want reason %q", tc.ref, s, tc.reason)
		}
	}
}

func TestSuccessorRangeUnsatisfied(t *testing.T) {
	q := successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil))
	d, meta := succDetail(t, q)
	if got := useOf(t, d, "app16").SuccessorRange; got != RangeUnsatisfied {
		t.Errorf("^16.0.0 vs 17.0.0 = %q", got)
	}
	if hasLimitationCode(meta, LimitationSuccessorUnresolved) {
		t.Error("a resolved successor raises no limitation")
	}
}

func TestSuccessorRangeSatisfied(t *testing.T) {
	q := successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil))
	d, _ := succDetail(t, q)
	if got := useOf(t, d, "wide").SuccessorRange; got != RangeSatisfied {
		t.Errorf(">=16 <18 vs 17.0.0 = %q", got)
	}
}

func TestSuccessorNoRangeUnknown(t *testing.T) {
	q := successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil))
	if got := successorRange("", "pg", q.latestSupported(q.snap.Services["pg"])); got != RangeUnknown {
		t.Errorf("no range = %q", got)
	}
	d, _ := succDetail(t, q)
	if u := useOf(t, d, "any"); u.SuccessorRange != RangeUnknown || u.SupportedRange != RangeUnknown {
		t.Errorf("a use with no range = %+v", u)
	}
}

// TestLatestSupportedFallback: a revision published before the deprecation
// is not supported, since a service lifecycle retires the whole service; only
// a newer revision that declares none is.
func TestLatestSupportedFallback(t *testing.T) {
	q := successorFleet(t, deprecated16)
	d, _ := succDetail(t, q)
	if p := d.Providers.Items[0]; p.LatestSupported != nil || useOf(t, d, "app16").SupportedRange != "" {
		t.Errorf("16.0.0 predates the deprecation: latest supported = %+v", p.LatestSupported)
	}
	q = successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil))
	d, _ = succDetail(t, q)
	if p := d.Providers.Items[0]; p.LatestSupported == nil || p.LatestSupported.Version != "17.0.0" {
		t.Fatalf("latest supported = %+v", p.LatestSupported)
	}
	if got := useOf(t, d, "app16").SupportedRange; got != RangeUnsatisfied {
		t.Errorf("^16 vs 17.0.0 = %q", got)
	}
}

// TestSuccessorOtherServiceNeverGraded: a successor that is another service
// says so; no consumer's range for the old provider is graded against it.
func TestSuccessorOtherServiceNeverGraded(t *testing.T) {
	aurora := &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "oci://ghcr.io/acme/aurora-pg:16.4.0"}
	q := successorFleet(t, aurora, lcRev("aurora-pg", "16.4.0", nil))
	d, _ := succDetail(t, q)
	var pg OfferingProvider
	for _, p := range d.Providers.Items {
		if p.Service.Key == "pg" {
			pg = p
		}
	}
	if s := pg.Successor; s == nil || s.Revision == nil || s.Reason != "the successor is another service, aurora-pg: change the dependency to it" {
		t.Errorf("successor = %+v", pg.Successor)
	}
	if got := useOf(t, d, "app16").SuccessorRange; got != RangeUnknown {
		t.Errorf("^16 vs aurora-pg 16.4.0 = %q", got)
	}
	items, err := q.Attention(AttentionFilter{Category: categoryLifecycle, Limit: MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items.Items {
		if it.Code == CodeSuccessorOutOfRange || !strings.Contains(it.NextStep, "dependency on pg to aurora-pg") {
			t.Errorf("item = %+v", it)
		}
	}
	res, _ := q.OfferingVersions("postgresql", "", "^16.0.0")
	for _, p := range res.Providers {
		if p.Provider.Key == "pg" && p.Versions.SuccessorRange != RangeUnknown {
			t.Errorf("versions = %+v", p.Versions)
		}
	}
}

// TestSuccessorRestrictedNeverAbsent: in a restricted view, a successor the
// viewer may not see is not called absent.
func TestSuccessorRestrictedNeverAbsent(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "data", "prod", time.Hour)
	a.target("pg-next", "n-0", "data-next", "prod", time.Hour)
	a.declares("other")
	a.target("other", "o-0", "data", "prod", time.Hour)
	q := a.query()
	if _, why := q.successorRevision(q.snap.Services["pg"], "oci://reg/platform/pg-next:17.0.0"); why != "no revision of pg-next in this fleet matches it" {
		t.Errorf("unrestricted reason = %q", why)
	}
	r := q.Restrict(func(ns string) bool { return ns != "data-next" }, nil)
	if _, why := r.successorRevision(r.snap.Services["pg"], "oci://reg/platform/pg-next:17.0.0"); why != "no service pg-next you may see in pg's registry domain is in this fleet" {
		t.Errorf("reason = %q", why)
	}
	if _, why := r.successorRevision(r.snap.Services["pg"], "oci://reg/platform/other:17.0.0"); why != "no revision of other you may see in this fleet matches it" {
		t.Errorf("reason = %q", why)
	}
}

func TestVersionsCarrySuccessorRange(t *testing.T) {
	q := successorFleet(t, deprecated16, lcRev("pg", "17.0.0", nil))
	res, err := q.OfferingVersions("postgresql", "", "^16.0.0")
	if err != nil {
		t.Fatal(err)
	}
	// ^16 reaches 16.1.0, whose successor 17.0.0 the range excludes, even
	// though the newest revision 17.0.0 declares nothing.
	if v := res.Providers[0].Versions; v.Successor == nil || v.Successor.Revision == nil || v.SuccessorRange != RangeUnsatisfied {
		t.Errorf("versions = %+v", v)
	}
	// Without a range and with no target, the newest readable revision
	// speaks: 17.0.0 names no successor.
	res, _ = q.OfferingVersions("postgresql", "", "")
	if v := res.Providers[0].Versions; v.Successor != nil || v.SuccessorRange != "" {
		t.Errorf("versions = %+v", v)
	}
	// A range that reaches nothing falls back to the newest revision.
	res, _ = q.OfferingVersions("postgresql", "", "^18.0.0")
	if v := res.Providers[0].Versions; v.Successor != nil {
		t.Errorf("versions = %+v", v)
	}
	q = successorFleet(t, &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "oci://ghcr.io/acme/pg:16.0.0"})
	res, _ = q.OfferingVersions("postgresql", "", "^16.0.0")
	if v := res.Providers[0].Versions; v.Successor == nil || v.Successor.Revision == nil || v.SuccessorRange != RangeSatisfied {
		t.Errorf("versions = %+v", v)
	}
	res, _ = q.OfferingVersions("postgresql", "", "")
	if v := res.Providers[0].Versions; v.Successor == nil || v.SuccessorRange != "" {
		t.Errorf("no range gives no successor range: %+v", v)
	}
	q = successorFleet(t, deprecated16)
	res, _ = q.OfferingVersions("postgresql", "", "^16.0.0")
	if v := res.Providers[0].Versions; v.Successor == nil || v.SuccessorRange != RangeUnknown {
		t.Errorf("an unresolved successor reads unknown: %+v", v)
	}
}

// TestVersionsSuccessorFromRunningRevision: a target still running 16.1.0
// makes it the provider's current revision, so its successor shows with no
// range, the provider reads deprecated and the lifecycle filter lists it.
func TestVersionsSuccessorFromRunningRevision(t *testing.T) {
	running := successorFleetRunning(t, "16.1.0", deprecated16, lcRev("pg", "17.0.0", nil))
	res, _ := running.OfferingVersions("postgresql", "", "")
	if v := res.Providers[0].Versions; v.Successor == nil || v.Successor.Revision == nil || v.SuccessorRange != "" {
		t.Errorf("running 16.1.0: versions = %+v", v)
	}
	d, _ := succDetail(t, running)
	if p := d.Providers.Items[0]; p.Lifecycle == nil || p.Successor == nil || p.LatestSupported == nil || p.LatestSupported.Label != "pg 17.0.0" {
		t.Errorf("running 16.1.0: provider lifecycle %+v successor %+v latest supported %+v", p.Lifecycle, p.Successor, p.LatestSupported)
	}
	if got := offeringRows(t, running, EntityFilter{Lifecycle: LifecycleDeprecated}); len(got) != 1 {
		t.Errorf("running 16.1.0: deprecated offerings = %+v", got)
	}
}

func TestLifecycleAttentionSuccessorOutOfRange(t *testing.T) {
	items := lifecycleItems(t, lifecycleRangeFleet(t))
	it := itemFor(items, "app16")
	if it == nil {
		t.Fatalf("no item for app16: %+v", items)
	}
	want := AttentionItem{
		Entity: it.Entity, Service: "app16", Label: "app16 uses pg", Severity: severityError,
		Code: CodeSuccessorOutOfRange, Category: categoryLifecycle, Summary: "Its range excludes the provider's successor",
		Reason:   "app16 accepts pg ^16.0.0, which excludes its successor 17.0.0",
		NextStep: "Widen the range in app16's contract to accept 17.0.0", NextAction: editContract(),
	}
	if !reflect.DeepEqual(*it, want) {
		t.Errorf("item = %+v\nwant %+v", *it, want)
	}
	sunset := &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-30", Successor: "oci://ghcr.io/acme/pg:17.0.0"}
	q := successorFleet(t, sunset, lcRev("pg", "17.0.0", nil))
	it = itemFor(lifecycleItems(t, q), "app16")
	if it == nil || it.Code != "DEPENDENCY_SERVICE_SUNSET" || it.NextStep != "Widen the range in app16's contract to accept 17.0.0" {
		t.Errorf("a past-sunset item keeps its code and gains the next step: %+v", it)
	}
}

func TestSuccessorRestrictedProviderHidden(t *testing.T) {
	q := lifecycleRangeFleet(t)
	r := q.Restrict(func(ns string) bool { return ns != "secret" }, nil)
	if r.snap.Services["pg"] != nil {
		t.Fatal("the fixture hides pg")
	}
	for _, rel := range r.snap.Relationships {
		if rel.ProviderLifecycle != nil {
			t.Errorf("a hidden provider exposes a lifecycle: %+v", rel)
		}
	}
	if items := lifecycleItems(t, r); len(items) != 0 {
		t.Errorf("a hidden provider raises no item: %+v", items)
	}
	d, err := r.EntityDetail(KindService, "app16")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Service.ConsumedOfferings.Items {
		if u.SuccessorRange != "" || u.SupportedRange != "" {
			t.Errorf("a hidden provider exposes a successor: %+v", u)
		}
	}
	var nf *NotFoundError
	if _, err := r.OfferingVersions("postgresql", "", ""); !errors.As(err, &nf) {
		t.Errorf("a hidden provider's versions = %v", err)
	}
}
