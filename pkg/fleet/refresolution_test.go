package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// Adversarial coverage for config/policy REFERENCE IDENTITY.
//
// A Product reference link asserts "this configuration comes from THAT service".
// The engine may only assert it from evidence of the destination's identity: the
// immutable content identifier pacto.lock recorded when it really resolved the
// referenced bundle, or a digest the author pinned in the ref itself. Everything
// else -- the repository leaf, the configuration scope name, the policy entry
// name -- is a label chosen by a human for a different purpose, and matching on
// it both misses real destinations and invents false ones.
//
// Unknown beats fabricated. The authored ref stays visible either way.

// refDigest is a syntactically valid, deterministic content digest for a fixture
// identity. Digest-pinned refs go through the same go-containerregistry grammar
// the resolver uses, so fixtures must use real 64-hex digests, not "sha256:x".
func refDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// refSnapshot builds two domains that each contain a "payments" service and a
// "checkout" service.
//
// Each checkout declares three references that between them cover every honest
// outcome:
//
//   - "settlement": a scope named nothing like its destination, pointing at a
//     repository named nothing like its destination either, with a LOCK entry
//     recording the digest it actually resolved to. Only authoritative evidence
//     can resolve this, and it must resolve inside its own domain.
//   - "nowhere": a ref nobody publishes and no lock covers. Unresolved.
//   - a policy named "payments" pointing at a retired bundle. The fleet HAS a
//     payments service; the policy entry name is not evidence that this is it, so
//     this must stay unresolved. It is the false positive the old heuristic
//     produced.
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
					{Name: "settlement", Ref: "oci://ghcr.io/" + domain + "/shared-config-contract:1.0.0", Required: true},
					{Name: "nowhere", Ref: "oci://example.com/nowhere:1.0.0"},
					{Name: "inline", Schema: "configuration/schema.json"},
				},
				Policies: []contract.Policy{
					{Name: "payments", Ref: "oci://ghcr.io/" + domain + "/retired-bundle:1.0.0", Target: "spend"},
					{Name: "local-only", Schema: "policy/schema.json", Target: "spend"},
				},
			}, FS: fstest.MapFS{}},
			Domain: domain, Digest: digest,
			Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
				Kind: contract.ReferenceKindConfig, Name: "settlement", Source: "oci",
				Ref:     "oci://ghcr.io/" + domain + "/shared-config-contract:1.0.0",
				Version: "1.0.0", Digest: refDigest(domain + "/payments"),
			}}},
		}
	}
	col := &Collection{Revisions: []RawRevision{
		plain("domain-a", "payments", refDigest("domain-a/payments")),
		plain("domain-b", "payments", refDigest("domain-b/payments")),
		checkout("domain-a", refDigest("domain-a/checkout")),
		checkout("domain-b", refDigest("domain-b/checkout")),
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

// A reference backed by an authoritative resolution exposes its canonical
// destination, and that destination is the one in the REFERRING revision's own
// domain -- never the same-named service next door.
func TestRefResolution_ResolvesWithinTheReferringDomain(t *testing.T) {
	snap := refSnapshot(t)
	q := NewQuery(snap)
	for _, domain := range []string{"domain-a", "domain-b"} {
		d := checkoutDetail(t, q, snap, domain)
		want := NewServiceKeyDomain(domain, "payments")

		// "settlement" is what the scope is CALLED and "shared-config-contract" is
		// what the repository is called; neither is the destination. The lock's
		// recorded digest is, and it names payments.
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
	}
}

// The policy entry name is a label for a slot in the REFERRING contract. A policy
// called "payments" whose ref points at a retired bundle is not a link to the
// payments service, however convenient the coincidence looks.
func TestRefResolution_PolicyEntryNameIsNotIdentity(t *testing.T) {
	snap := refSnapshot(t)
	q := NewQuery(snap)
	for _, domain := range []string{"domain-a", "domain-b"} {
		d := checkoutDetail(t, q, snap, domain)
		pol := findPolicy(t, d.Policies, "payments")
		if pol.Ref == "" {
			t.Errorf("%s: the authored ref must stay visible", domain)
		}
		if pol.Resolution == nil {
			t.Fatalf("%s: a policy WITH a ref must carry a verdict", domain)
		}
		if pol.Resolution.Resolved || pol.Resolution.Service != nil {
			t.Errorf("%s: a policy entry name must not become a canonical destination, got %+v", domain, pol.Resolution)
		}
		if pol.Resolution.Reason == "" {
			t.Errorf("%s: the unresolved verdict must say why", domain)
		}
	}
}

// basenameSnapshot is the repo-name-is-not-service-name fixture: the referenced
// bundle lives in a repository called "shared-config-contract" but publishes a
// contract whose service.name is "platform-settings" -- AND the fleet separately
// contains an unrelated service that really is called "shared-config-contract".
// The leaf therefore points at the wrong service and away from the right one.
func basenameSnapshot(t *testing.T, appLock *lock.Lock, appRef string) *FleetSnapshot {
	t.Helper()
	app := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{{Name: "settings", Ref: appRef}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: refDigest("app"), Lock: appLock},
		{Bundle: bundleFor(t, "platform-settings"), Digest: refDigest("platform-settings")},
		{Bundle: bundleFor(t, "shared-config-contract"), Digest: refDigest("decoy")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// Adversarial 1 and 2: the destination comes from the resolved identity, and the
// unrelated service whose name happens to equal the repository leaf is not it.
func TestRefResolution_RepositoryBasenameIsNotIdentity(t *testing.T) {
	ref := "oci://ghcr.io/acme/shared-config-contract:2.1.0"
	snap := basenameSnapshot(t, &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
		Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci", Ref: ref,
		Version: "2.1.0", Digest: refDigest("platform-settings"),
	}}}, ref)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || !rel.Resolved {
		t.Fatalf("an authoritatively locked reference must resolve: %+v", rel)
	}
	if rel.ToService != NewServiceKey("platform-settings") {
		t.Errorf("resolved to %q, want the referenced bundle's real service.name platform-settings", rel.ToService)
	}
	if rel.ResolvedRevision == "" {
		t.Error("an identity-resolved reference knows the exact destination revision")
	}
	if rel.LockedDigest != refDigest("platform-settings") {
		t.Errorf("the edge must carry the lock digest it resolved through, got %q", rel.LockedDigest)
	}
}

// Adversarial 2, stated on its own: with NO authoritative evidence, the presence
// of a service named after the repository leaf must change nothing.
func TestRefResolution_UnrelatedRepoLeafCollisionIsNotALink(t *testing.T) {
	snap := basenameSnapshot(t, nil, "oci://ghcr.io/acme/shared-config-contract:2.1.0")

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil {
		t.Fatal("an unresolved reference still emits an edge")
	}
	if rel.Resolved || rel.ToService != "" {
		t.Fatalf("a repository leaf must never be promoted to a ServiceKey: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved edge must say why")
	}
}

// Adversarial 5: a contentHash identifies an exact known revision just as a
// registry digest does.
func TestRefResolution_ContentHashIdentifiesTheRevision(t *testing.T) {
	ref := "oci://ghcr.io/acme/shared-config-contract:2.1.0"
	snap := basenameSnapshot(t, &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
		Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci", Ref: ref,
		ContentHash: refDigest("platform-settings"),
	}}}, ref)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || !rel.Resolved || rel.ToService != NewServiceKey("platform-settings") {
		t.Errorf("a contentHash naming a known revision resolves it: %+v", rel)
	}
}

// A ref the AUTHOR pinned to a digest is itself a content address: no lock needed.
func TestRefResolution_DigestPinnedRefResolvesWithoutALock(t *testing.T) {
	snap := basenameSnapshot(t, nil, "oci://ghcr.io/acme/shared-config-contract@"+refDigest("platform-settings"))

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || !rel.Resolved || rel.ToService != NewServiceKey("platform-settings") {
		t.Errorf("a digest-pinned ref names exactly one bundle: %+v", rel)
	}
}

// Adversarial 6: the identity is authoritative but its bundle is not in this
// snapshot. That is an honest unknown, not a nearest-match.
func TestRefResolution_AuthoritativeTargetAbsentIsUnresolved(t *testing.T) {
	ref := "oci://ghcr.io/acme/shared-config-contract:2.1.0"
	snap := basenameSnapshot(t, &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
		Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci", Ref: ref,
		Digest: refDigest("a-bundle-nobody-collected"),
	}}}, ref)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("an absent destination must stay unresolved: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved edge must say why")
	}
	// The evidence is still reported: the reader can see WHICH bundle was meant.
	if rel.LockedDigest == "" {
		t.Error("the lock digest is evidence and must survive an unresolved verdict")
	}
}

// Adversarial 4: an identity that matches a revision in ANOTHER domain resolves
// to nothing. Domains are separate identity spaces, and a reference cannot leave
// its own.
func TestRefResolution_CrossDomainIdentityDoesNotResolve(t *testing.T) {
	ref := "oci://ghcr.io/acme/shared-config-contract:1.0.0"
	app := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{{Name: "settings", Ref: ref}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Domain: "east", Digest: refDigest("app"),
			Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
				Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci", Ref: ref,
				Digest: refDigest("west/platform-settings"),
			}}}},
		{Bundle: bundleFor(t, "platform-settings"), Domain: "west", Digest: refDigest("west/platform-settings")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}

	rel := relFrom(snap.Relationships, string(NewServiceKeyDomain("east", "app")), "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("a reference must not resolve across domains: %+v", rel)
	}
}

// One content identity claimed by two different services in one domain cannot
// pick a winner: neither is more right than the other.
func TestRefResolution_AmbiguousIdentityIsNotArbitrarilyWon(t *testing.T) {
	shared := refDigest("shared-identity")
	ref := "oci://ghcr.io/acme/whatever@" + shared
	app := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{{Name: "settings", Ref: ref}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: refDigest("app")},
		{Bundle: bundleFor(t, "one"), Digest: shared},
		{Bundle: bundleFor(t, "two"), Digest: shared},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("an ambiguous identity must not be arbitrarily won: %+v", rel)
	}
}

// Adversarial 9: reference identity got stricter; DEPENDENCY resolution did not
// change. A declared dependency still resolves by name inside its own domain,
// with or without a lock.
func TestRefResolution_DependencySemanticsAreUnchanged(t *testing.T) {
	web := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "web", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{{Name: "leaf", Ref: "oci://ex/leaf", Required: true, Compatibility: "^1.0.0"}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: web, FS: fstest.MapFS{}}, Digest: refDigest("web")},
		{Bundle: bundleFor(t, "leaf"), Digest: refDigest("leaf")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	rel := relFrom(snap.Relationships, "web", "leaf")
	if rel == nil || !rel.Resolved || rel.ToService != NewServiceKey("leaf") {
		t.Errorf("an unlocked dependency must still resolve by name: %+v", rel)
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

// Adversarial 7: ReferencedBy is built from authoritative resolved edges only, so
// a service is never listed as referenced by a contract that merely mentions its
// name.
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

// The decoy service in basenameSnapshot must never appear in ReferencedBy either:
// the false link is absent from BOTH directions of the relationship.
func TestReferencedBy_ExcludesNameCollisionDestinations(t *testing.T) {
	snap := basenameSnapshot(t, nil, "oci://ghcr.io/acme/shared-config-contract:2.1.0")
	d, err := NewQuery(snap).EntityDetail(KindService, string(NewServiceKey("shared-config-contract")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.ReferencedBy.Total != 0 {
		t.Errorf("a name collision must not make a service look referenced: %+v", d.Service.ReferencedBy)
	}
}
