package fleet

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// refSnapshot builds two domains that each contain a "payments" service and a
// "checkout" service. Both checkouts reference payments twice: once through a
// scope whose declared name is nothing like the service ("settlement"), which is
// the realistic shape and the one only the REF can resolve, and once through a
// policy whose ref leads nowhere but whose declared name matches the service,
// which is the fallback. A third reference resolves to nothing at all.
//
// The fixture exists to prove a reference resolves inside its OWN domain and that
// an unresolvable reference stays honestly unresolved.
func refSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	plain := func(domain, name, digest string) RawRevision {
		return RawRevision{
			Bundle: &contract.Bundle{Contract: &contract.Contract{
				PactoVersion: "2.0",
				Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
			}, FS: fstest.MapFS{}},
			Domain: domain, Digest: digest,
		}
	}
	checkout := func(domain, digest string) RawRevision {
		return RawRevision{
			Bundle: &contract.Bundle{Contract: &contract.Contract{
				PactoVersion: "2.0",
				Service:      contract.Service{Name: "checkout", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
				Configurations: []contract.Configuration{
					{Name: "settlement", Ref: "oci://" + domain + "/payments:1.0.0", Required: true},
					{Name: "nowhere", Ref: "oci://example.com/nowhere:1.0.0"},
					{Name: "inline", Schema: "configuration/schema.json"},
				},
				Policies: []contract.Policy{
					{Name: "payments", Ref: "oci://" + domain + "/retired-bundle:1.0.0", Target: "spend"},
					{Name: "local-only", Schema: "policy/schema.json", Target: "spend"},
				},
			}, FS: fstest.MapFS{}},
			Domain: domain, Digest: digest,
		}
	}
	col := &Collection{Revisions: []RawRevision{
		plain("domain-a", "payments", "sha256:a-pay"),
		plain("domain-b", "payments", "sha256:b-pay"),
		checkout("domain-a", "sha256:a-chk"),
		checkout("domain-b", "sha256:b-chk"),
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func checkoutDetail(t *testing.T, q *Query, snap *FleetSnapshot, domain string) *RevisionDetailData {
	t.Helper()
	svc := snap.Services[NewServiceKeyDomain(domain, "checkout")]
	if svc == nil || len(svc.Revisions) != 1 {
		t.Fatalf("fixture: %s/checkout", domain)
	}
	d, err := q.EntityDetail(KindRevision, string(svc.Revisions[0]))
	if err != nil {
		t.Fatal(err)
	}
	return d.Revision
}

func findConfig(t *testing.T, p ConfigurationsPreview, name string) ConfigurationSummary {
	t.Helper()
	for _, c := range p.Items {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no configuration %q", name)
	return ConfigurationSummary{}
}

func findPolicy(t *testing.T, p PoliciesPreview, name string) PolicySummary {
	t.Helper()
	for _, s := range p.Items {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no policy %q", name)
	return PolicySummary{}
}

// A reference that the builder resolved must expose its canonical destination,
// and that destination must be the one in the REFERRING revision's own domain --
// never the same-named service next door.
func TestRefResolution_ResolvesWithinTheReferringDomain(t *testing.T) {
	snap := refSnapshot(t)
	q := NewQuery(snap)
	for _, domain := range []string{"domain-a", "domain-b"} {
		d := checkoutDetail(t, q, snap, domain)
		want := NewServiceKeyDomain(domain, "payments")

		// "settlement" is what the scope is CALLED; "payments" is what it points at.
		// Only the ref can bridge the two, which is exactly what used to be missing.
		cfg := findConfig(t, d.Configurations, "settlement")
		if cfg.Ref == "" {
			t.Errorf("%s: the authored ref must stay visible alongside the resolution", domain)
		}
		if cfg.Resolution == nil || !cfg.Resolution.Resolved || cfg.Resolution.Service == nil {
			t.Fatalf("%s: configuration ref should resolve, got %+v", domain, cfg.Resolution)
		}
		if cfg.Resolution.Service.Key != string(want) {
			t.Errorf("%s: configuration resolved to %q, want %q", domain, cfg.Resolution.Service.Key, want)
		}
		if cfg.Resolution.Service.Kind != KindService {
			t.Errorf("%s: destination must be a canonical service ref, got kind %q", domain, cfg.Resolution.Service.Kind)
		}

		// The policy's ref names a bundle nobody publishes, but the policy is declared
		// under the name of the service that owns it -- the older convention, still
		// resolvable and still domain-scoped.
		pol := findPolicy(t, d.Policies, "payments")
		if pol.Resolution == nil || pol.Resolution.Service == nil || pol.Resolution.Service.Key != string(want) {
			t.Errorf("%s: policy resolved to %+v, want %q", domain, pol.Resolution, want)
		}
	}
}

// The leaf of the ref is the only part that can name a service. Everything the
// registry adds around it -- scheme, host, port, org path, tag, digest -- must
// come off, and nothing may be invented when there is no leaf to read.
func TestRefServiceName(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"oci://ghcr.io/trianalab/pacto/platform-app-config", "platform-app-config"},
		{"oci://registry:5000/platform-app-config:1.2.3", "platform-app-config"},
		{"oci://ghcr.io/acme/cfg@sha256:" + strings.Repeat("a", 64), "cfg"},
		{"oci://ghcr.io/acme/cfg/", "cfg"},
		{"file://../shared/config", "config"},
		{"platform-app-config", "platform-app-config"},
		{"", ""},
	} {
		if got := refServiceName(tc.ref); got != tc.want {
			t.Errorf("refServiceName(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// A bundle published as "<service>-pacto" is the same service. The tolerance is
// shared with dependency resolution, so a reference gets it too.
func TestRefResolution_ToleratesThePactoBundleSuffix(t *testing.T) {
	app := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{
			{Name: "shared", Ref: "oci://ghcr.io/acme/platform-config-pacto:1.0.0"},
		},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: "sha256:app"},
		{Bundle: bundleFor(t, "platform-config"), Digest: "sha256:cfg"},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	rel := relFrom(snap.Relationships, "app", "shared")
	if rel == nil || !rel.Resolved || rel.ToService != "platform-config" {
		t.Errorf("suffixed bundle ref should resolve to the service: %+v", rel)
	}
}

func TestRefResolution_UnresolvedIsHonestAndLocalScopesCarryNone(t *testing.T) {
	snap := refSnapshot(t)
	d := checkoutDetail(t, NewQuery(snap), snap, "domain-a")

	nowhere := findConfig(t, d.Configurations, "nowhere")
	if nowhere.Ref == "" {
		t.Error("an unresolved reference must still show the authored ref")
	}
	if nowhere.Resolution == nil || nowhere.Resolution.Resolved {
		t.Fatalf("want an unresolved resolution, got %+v", nowhere.Resolution)
	}
	if nowhere.Resolution.Service != nil {
		t.Error("an unresolved reference must not fabricate a destination service")
	}
	if nowhere.Resolution.Reason == "" {
		t.Error("an unresolved reference must say why")
	}

	// A scope with no ref declares no reference at all, so it carries no verdict:
	// absent is different from unresolved.
	if inline := findConfig(t, d.Configurations, "inline"); inline.Resolution != nil {
		t.Errorf("a ref-less configuration must carry no resolution, got %+v", inline.Resolution)
	}
	if local := findPolicy(t, d.Policies, "local-only"); local.Resolution != nil {
		t.Errorf("a ref-less policy must carry no resolution, got %+v", local.Resolution)
	}
}

// A resolved edge whose service vanished from the snapshot must degrade to an
// honest unresolved verdict rather than a link to nothing.
func TestRefResolution_MissingDestinationDegradesToUnresolved(t *testing.T) {
	snap := refSnapshot(t)
	delete(snap.Services, NewServiceKeyDomain("domain-a", "payments"))

	d := checkoutDetail(t, NewQuery(snap), snap, "domain-a")
	cfg := findConfig(t, d.Configurations, "settlement")
	if cfg.Resolution == nil || cfg.Resolution.Resolved || cfg.Resolution.Service != nil {
		t.Fatalf("want an unresolved resolution with no service, got %+v", cfg.Resolution)
	}
	if cfg.Resolution.Reason == "" {
		t.Error("the degraded verdict must explain itself")
	}
}

// References are not dependencies, so the referenced service would otherwise
// never list the services that reference it.
func TestReferencedBy_ListsReferencingServicesWithinTheDomain(t *testing.T) {
	snap := refSnapshot(t)
	q := NewQuery(snap)
	for _, domain := range []string{"domain-a", "domain-b"} {
		d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain(domain, "payments")))
		if err != nil {
			t.Fatal(err)
		}
		got := d.Service.ReferencedBy
		if got.Total != 1 || len(got.Items) != 1 {
			t.Fatalf("%s: want exactly one referencing service, got %+v", domain, got)
		}
		if want := string(NewServiceKeyDomain(domain, "checkout")); got.Items[0].Key != want {
			t.Errorf("%s: referenced by %q, want %q", domain, got.Items[0].Key, want)
		}
	}

	// The referencing service itself is referenced by nobody.
	d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain("domain-a", "checkout")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.ReferencedBy.Total != 0 {
		t.Errorf("checkout should be referenced by nobody, got %+v", d.Service.ReferencedBy)
	}
}
