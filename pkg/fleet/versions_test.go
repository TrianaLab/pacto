package fleet

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// versionsFleet holds one postgresql provider with versions 15.0.0, 16.0.0
// (deprecated), 16.1.0, latest (not semver) and an invalid 17.0.0, and a second
// provider pg-alt of the same offering.
func versionsFleet(t *testing.T) *Query {
	t.Helper()
	rev := func(name, version string) *contract.Contract {
		c := svc25(name, version, "postgresql")
		c.Workload = contract.WorkloadService
		return c
	}
	old := rev("postgresql", "16.0.0")
	old.PactoVersion = "2.6"
	old.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-03-01", Successor: "oci://ghcr.io/acme/postgresql:16.1.0"}
	bad := mustParse(t, invalidYAML)
	bad.PactoVersion, bad.Service.Name, bad.Service.Version, bad.Service.Offering = "2.5", "postgresql", "17.0.0", "postgresql"
	invalid := ociRev("ghcr.io/acme", bad, nil)
	invalid.Bundle.RawYAML = []byte(strings.ReplaceAll(strings.ReplaceAll(invalidYAML, "bad-svc", "postgresql"), "1.0.0", "17.0.0"))
	return buildDomainSnap(t, "oci", &Collection{
		Revisions: []RawRevision{
			ociRev("ghcr.io/acme", rev("postgresql", "15.0.0"), nil), ociRev("ghcr.io/acme", old, nil),
			ociRev("ghcr.io/acme", rev("postgresql", "16.1.0"), nil), ociRev("ghcr.io/acme", rev("postgresql", "latest"), nil),
			invalid, ociRev("ghcr.io/acme", rev("pg-alt", "1.0.0"), nil),
		},
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pg", Service: "postgresql", Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", "postgresql", "15.0.0")}},
	})
}

func versionLine(v ProviderVersion) string {
	s := v.Version + ":" + v.RangeStatus
	if v.InUse != nil && *v.InUse {
		s += ":in-use"
	}
	if v.Lifecycle != nil {
		s += ":deprecated"
	}
	if v.SunsetReached {
		s += ":sunset"
	}
	return s
}

func TestProviderVersions(t *testing.T) {
	q := versionsFleet(t)
	pg := acmeService(q, "postgresql")
	for _, tc := range []struct {
		rng, latest string
		want        []string
	}{
		// Newest first in revision chronology; an invalid revision and a version semver cannot read are unknown.
		{"^16.0.0", "16.1.0", []string{"latest:unknown", "17.0.0:unknown", "16.1.0:satisfied", "16.0.0:satisfied:deprecated:sunset", "15.0.0:unsatisfied:in-use"}},
		// The latest compatible skips an invalid revision that satisfies the range.
		{">=15.0.0", "16.1.0", []string{"latest:unknown", "17.0.0:unknown", "16.1.0:satisfied", "16.0.0:satisfied:deprecated:sunset", "15.0.0:satisfied:in-use"}},
		{"^18.0.0", "", []string{"latest:unknown", "17.0.0:unknown", "16.1.0:unsatisfied", "16.0.0:unsatisfied:deprecated:sunset", "15.0.0:unsatisfied:in-use"}},
		// No range, no statuses.
		{"", "", []string{"latest:", "17.0.0:", "16.1.0:", "16.0.0::deprecated:sunset", "15.0.0::in-use"}},
	} {
		p := q.offeringProviderVersions(pg, tc.rng)
		var got []string
		for _, v := range p.Items {
			got = append(got, versionLine(v))
		}
		latest := ""
		if p.LatestCompatible != nil {
			latest = p.LatestCompatible.Version
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) || latest != tc.latest || p.Range != tc.rng || p.Total != 5 || p.Completeness != "fleet" || p.Note != providerVersionsNote {
			t.Errorf("%q: versions = %v latest %q, want %v latest %q (%+v)", tc.rng, got, latest, tc.want, tc.latest, p)
		}
	}
}

func TestProviderVersions_Bounded(t *testing.T) {
	var revs []RawRevision
	for i := range MaxDetailPreview + 3 {
		revs = append(revs, offerRev("kv", fmt.Sprintf("1.%d.0", i), "cache", ""))
	}
	q := buildDomainSnap(t, "local", &Collection{Revisions: revs})
	p := q.offeringProviderVersions(acmeService(q, "kv"), "")
	if p.Count != MaxDetailPreview || len(p.Items) != MaxDetailPreview || p.Total != MaxDetailPreview+3 || !p.Truncated || p.Items[0].Version != fmt.Sprintf("1.%d.0", MaxDetailPreview+2) {
		t.Errorf("versions = %d of %d (truncated %v), newest %s", p.Count, p.Total, p.Truncated, p.Items[0].Version)
	}
}

func TestOfferingVersions(t *testing.T) {
	q := versionsFleet(t)
	res, err := q.OfferingVersions("postgresql", "", "^16.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Offering.Key != "postgresql" || res.Total != 2 || res.Truncated || len(res.Providers) != 2 || res.Providers[0].Provider.Label != "pg-alt" ||
		res.Providers[1].Versions.LatestCompatible == nil || res.Providers[1].Versions.LatestCompatible.Version != "16.1.0" ||
		res.Providers[0].Versions.LatestCompatible != nil || res.Meta.AsOf.IsZero() {
		t.Errorf("result = %+v", res)
	}
}

func TestOfferingVersions_NarrowedAndCloned(t *testing.T) {
	q := versionsFleet(t)
	res, _ := q.OfferingVersions("postgresql", "", "")
	// The answer is a copy: mutating it leaves the next one alone.
	res.Providers[1].Versions.Items[3].Lifecycle.Deprecated = "mutated"
	if again, _ := q.OfferingVersions("postgresql", "postgresql", ""); len(again.Providers) != 1 || again.Providers[0].Versions.Items[3].Lifecycle.Deprecated != "2026-01-01" {
		t.Errorf("narrowed result = %+v", again)
	}
	if res, err := q.OfferingVersions("postgresql", string(NewServiceKeyDomain("ghcr.io/acme", "pg-alt")), ""); err != nil || len(res.Providers) != 1 || res.Providers[0].Provider.Label != "pg-alt" {
		t.Errorf("narrowed by key: %+v, %v", res, err)
	}
}

func TestOfferingVersions_Errors(t *testing.T) {
	q := versionsFleet(t)
	var bad *InvalidQueryError
	if _, err := q.OfferingVersions("postgresql", "", "not a range"); !errors.As(err, &bad) || bad.Field != "range" || !strings.Contains(err.Error(), "not a semver range") {
		t.Errorf("bad range: %v", err)
	}
	var nf *NotFoundError
	for _, tc := range [][2]string{{"nope", ""}, {"", ""}, {"postgresql", "nobody"}} {
		if _, err := q.OfferingVersions(tc[0], tc[1], ""); !errors.As(err, &nf) {
			t.Errorf("%v: err = %v, want NotFoundError", tc, err)
		}
	}
	q = buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{offerRev("postgresql", "1.0.0", "postgresql", ""), offerRev("redis", "1.0.0", "redis", "")}})
	if _, err := q.OfferingVersions("postgresql", "redis", ""); !errors.As(err, &nf) || !strings.Contains(err.Error(), "provider of postgresql") {
		t.Errorf("a provider of another offering: %v", err)
	}
}

func TestOfferingVersions_ProvidersBounded(t *testing.T) {
	var revs []RawRevision
	for i := range MaxOfferingProviders + 2 {
		revs = append(revs, offerRev(fmt.Sprintf("p%02d", i), "1.0.0", "shared", ""))
	}
	res, err := buildDomainSnap(t, "local", &Collection{Revisions: revs}).OfferingVersions("shared", "", "")
	if err != nil || res.Total != MaxOfferingProviders+2 || !res.Truncated || len(res.Providers) != MaxOfferingProviders {
		t.Errorf("result = %d of %d, %v", len(res.Providers), res.Total, err)
	}
}

// A provider a restricted viewer sees only by its declared offering shows its
// versions without saying whether a target runs them.
func TestProviderVersionsInUseHiddenForOfferingOnly(t *testing.T) {
	q := offeringRestrictFleet(t)
	if v := offeringData(t, q, "relational-db").Providers.Items[0].Versions.Items[0]; v.InUse == nil {
		t.Errorf("unrestricted version = %+v, want inUse set", v)
	}
	if v := offeringData(t, q.Restrict(shopOnly), "relational-db").Providers.Items[0].Versions.Items[0]; v.InUse != nil || v.Version != "1.0.0" {
		t.Errorf("restricted version = %+v, want inUse absent", v)
	}
}

// A provider with a target in a namespace the viewer cannot read never says
// "not in use" for any version: the hidden target may run it.
func TestProviderVersionsInUseHiddenForPartlyVisibleProvider(t *testing.T) {
	rev := func(version string) RawRevision {
		c := svc25("postgresql", version, "postgresql")
		c.Workload = contract.WorkloadService
		return ociRev("ghcr.io/acme", c, nil)
	}
	target := func(ns, version string) RawTarget {
		return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: "pg", Service: "postgresql", Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", "postgresql", version)}
	}
	q := buildDomainSnap(t, "oci", &Collection{
		Revisions: []RawRevision{rev("15.0.0"), rev("16.1.0")},
		Targets:   []RawTarget{target("visible", "16.1.0"), target("secret", "15.0.0")},
	})
	for _, v := range q.offeringProviderVersions(acmeService(q, "postgresql"), "").Items {
		if v.InUse == nil || !*v.InUse {
			t.Errorf("unrestricted %s = %+v, want in use", v.Version, v.InUse)
		}
	}
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	for _, v := range r.offeringProviderVersions(acmeService(r, "postgresql"), "").Items {
		if v.InUse != nil {
			t.Errorf("restricted %s inUse = %v, want absent", v.Version, *v.InUse)
		}
	}
}
