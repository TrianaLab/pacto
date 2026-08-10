package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// Adversarial coverage for REFERENCE OCCURRENCE identity.
//
// refresolution_test.go establishes that a canonical destination may only come
// from an authoritative immutable identity. This file establishes the other half
// of the same claim: that the identity used must be the one recorded for THIS
// declared reference occurrence.
//
// pacto.lock holds the TRANSITIVE reference closure, so one lockfile routinely
// carries several entries with the same (kind, name): a configuration scope
// called "settings" declared by the root contract, and another called "settings"
// declared by a bundle the root reached through some other reference. Their
// digests are both authoritative -- for different reference occurrences. Picking
// between them by label produces a digest that is perfectly real and names the
// wrong bundle, which is worse than an unknown: it renders as a confident,
// canonical Product link to a service the contract never referenced.
//
// The counterexample below is the minimal shape that produces it.

// occurrenceCollision is the counterexample topology.
//
//	app       config "foo"      -> child-a
//	app       config "settings" -> bundle-y     (THE reference under test)
//	child-a   config "settings" -> bundle-x
//
// and the policy mirror of the same shape. buildReferenceClosure walks depth
// first, so child-a's "settings" is pinned into app's lock BEFORE app's own
// "settings" -- and after a Marshal round trip the entries are sorted by kind,
// name then ref, which orders bundle-x ahead of bundle-y just the same. Neither
// slice order nor sorted order carries any information about which contract
// declared which entry.
//
// The fleet must project app's own "settings" onto bundle-y. Nothing about the
// lock's ordering is allowed to matter.
func occurrenceCollision(t *testing.T) *FleetSnapshot {
	t.Helper()
	const (
		childARef  = "oci://ghcr.io/acme/child-a:1.0.0"
		bundleXRef = "oci://ghcr.io/acme/bundle-x:1.0.0"
		bundleYRef = "oci://ghcr.io/acme/bundle-y:1.0.0"
		childBRef  = "oci://ghcr.io/acme/child-b:1.0.0"
		bundlePRef = "oci://ghcr.io/acme/bundle-p:1.0.0"
		bundleQRef = "oci://ghcr.io/acme/bundle-q:1.0.0"
	)
	app := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{
			{Name: "foo", Ref: childARef},
			{Name: "settings", Ref: bundleYRef},
		},
		Policies: []contract.Policy{
			{Name: "bar", Ref: childBRef, Target: "spend"},
			{Name: "guardrails", Ref: bundleQRef, Target: "spend"},
		},
	}
	// The lock records the whole closure, in the order buildReferenceClosure
	// emits it: depth first, so each child's same-named entry lands first.
	appLock := &lock.Lock{
		LockVersion: lock.CurrentLockVersion,
		Root:        lock.RootInfo{Name: "app", Version: "1.0.0"},
		References: []lock.Reference{
			{From: "", Kind: contract.ReferenceKindPolicy, Name: "bar", Source: "oci",
				Ref: childBRef, Version: "1.0.0", Digest: refDigest("child-b")},
			{From: "policy:bar", Kind: contract.ReferenceKindPolicy, Name: "guardrails", Source: "oci",
				Ref: bundlePRef, Version: "1.0.0", Digest: refDigest("bundle-p")},
			{From: "", Kind: contract.ReferenceKindPolicy, Name: "guardrails", Source: "oci",
				Ref: bundleQRef, Version: "1.0.0", Digest: refDigest("bundle-q")},
			{From: "", Kind: contract.ReferenceKindConfig, Name: "foo", Source: "oci",
				Ref: childARef, Version: "1.0.0", Digest: refDigest("child-a")},
			{From: "config:foo", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
				Ref: bundleXRef, Version: "1.0.0", Digest: refDigest("bundle-x")},
			{From: "", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
				Ref: bundleYRef, Version: "1.0.0", Digest: refDigest("bundle-y")},
		},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: refDigest("app"), Lock: appLock},
		{Bundle: bundleFor(t, "child-a"), Digest: refDigest("child-a")},
		{Bundle: bundleFor(t, "child-b"), Digest: refDigest("child-b")},
		{Bundle: bundleFor(t, "bundle-x"), Digest: refDigest("bundle-x")},
		{Bundle: bundleFor(t, "bundle-y"), Digest: refDigest("bundle-y")},
		{Bundle: bundleFor(t, "bundle-p"), Digest: refDigest("bundle-p")},
		{Bundle: bundleFor(t, "bundle-q"), Digest: refDigest("bundle-q")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// THE counterexample: a transitive lock entry that shares a configuration scope
// name with a direct one must not be projected onto the direct reference.
func TestRefOccurrence_TransitiveConfigDoesNotAnswerForADirectReference(t *testing.T) {
	snap := occurrenceCollision(t)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || !rel.Resolved {
		t.Fatalf("app's own settings reference is authoritatively locked and must resolve: %+v", rel)
	}
	if rel.ToService != NewServiceKey("bundle-y") {
		t.Errorf("app's settings resolved to %q, want bundle-y; a configuration scope declared by child-a is not app's",
			rel.ToService)
	}
	if rel.LockedDigest != refDigest("bundle-y") {
		t.Errorf("edge carries locked digest %q, want the one recorded for app's OWN settings occurrence",
			rel.LockedDigest)
	}
}

// The policy mirror: policy entry names collide across the closure exactly the
// same way, and a transitive policy entry is no more app's than a config one.
func TestRefOccurrence_TransitivePolicyDoesNotAnswerForADirectReference(t *testing.T) {
	snap := occurrenceCollision(t)

	rel := relFrom(snap.Relationships, "app", "guardrails")
	if rel == nil || !rel.Resolved {
		t.Fatalf("app's own guardrails policy is authoritatively locked and must resolve: %+v", rel)
	}
	if rel.ToService != NewServiceKey("bundle-q") {
		t.Errorf("app's guardrails resolved to %q, want bundle-q; a policy entry declared by child-b is not app's",
			rel.ToService)
	}
	if rel.LockedDigest != refDigest("bundle-q") {
		t.Errorf("edge carries locked digest %q, want the one recorded for app's OWN guardrails occurrence",
			rel.LockedDigest)
	}
}

// The direct references that do NOT collide keep resolving: fixing the collision
// must not cost the feature. This is the "do not weaken the transitive lock"
// floor stated as a test.
func TestRefOccurrence_NonCollidingDirectReferencesStillResolve(t *testing.T) {
	snap := occurrenceCollision(t)

	for _, tc := range []struct{ name, want string }{{"foo", "child-a"}, {"bar", "child-b"}} {
		rel := relFrom(snap.Relationships, "app", tc.name)
		if rel == nil || !rel.Resolved || rel.ToService != NewServiceKey(tc.want) {
			t.Errorf("%s resolved to %+v, want %s", tc.name, rel, tc.want)
		}
	}
}

// occurrenceLock builds a single-reference lock for "app" so a test can vary one
// dimension of the recorded occurrence identity at a time.
func occurrenceLock(version int, refs ...lock.Reference) *lock.Lock {
	return &lock.Lock{
		LockVersion: version,
		Root:        lock.RootInfo{Name: "app", Version: "1.0.0"},
		References:  refs,
	}
}

// occurrenceApp builds the app revision plus a "platform-settings" destination,
// so only the lock under test varies.
func occurrenceApp(t *testing.T, l *lock.Lock) *FleetSnapshot {
	t.Helper()
	const ref = "oci://ghcr.io/acme/shared-config-contract:2.1.0"
	app := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{{Name: "settings", Ref: ref}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: refDigest("app"), Lock: l},
		{Bundle: bundleFor(t, "platform-settings"), Digest: refDigest("platform-settings")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// A lock written before reference-occurrence identity existed records no
// declaring contract, so no entry in it can be shown to belong to this declared
// reference. Unknown beats a plausible wrong link.
func TestRefOccurrence_LegacyLockDegradesToUnresolved(t *testing.T) {
	snap := occurrenceApp(t, occurrenceLock(1, lock.Reference{
		Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
		Ref: "oci://ghcr.io/acme/shared-config-contract:2.1.0", Digest: refDigest("platform-settings"),
	}))

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil {
		t.Fatal("an unresolved reference still emits an edge")
	}
	if rel.Resolved || rel.ToService != "" {
		t.Fatalf("a lock with no occurrence identity must not produce a canonical link: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved verdict must say why")
	}
}

// Two entries recording the SAME declared occurrence contradict each other. That
// is ambiguity, not a tie to break.
func TestRefOccurrence_DuplicateOccurrenceEntriesAreAmbiguous(t *testing.T) {
	const ref = "oci://ghcr.io/acme/shared-config-contract:2.1.0"
	snap := occurrenceApp(t, occurrenceLock(lock.CurrentLockVersion,
		lock.Reference{From: "", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
			Ref: ref, Digest: refDigest("platform-settings")},
		lock.Reference{From: "", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
			Ref: ref, Digest: refDigest("something-else")},
	))

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("contradictory occurrence entries must stay unresolved: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved verdict must say why")
	}
}

// A lock belongs to exactly one root contract. Attached to a different revision
// -- a source bug, or a bundle assembled by hand -- its "declared by the root"
// entries describe someone else's root, so they answer for nothing here.
func TestRefOccurrence_LockFromAnotherContractAnswersForNothing(t *testing.T) {
	l := occurrenceLock(lock.CurrentLockVersion, lock.Reference{
		From: "", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
		Ref: "oci://ghcr.io/acme/shared-config-contract:2.1.0", Digest: refDigest("platform-settings"),
	})
	l.Root = lock.RootInfo{Name: "some-other-service", Version: "9.9.9"}
	snap := occurrenceApp(t, l)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("another contract's lock must not link this one: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved verdict must say why")
	}
	if rel.LockedDigest != "" {
		t.Errorf("no pin may be carried over from another contract's lock, got %q", rel.LockedDigest)
	}
}

// The same, one dimension weaker: a lock that names no contract but a version
// that is not this revision's still describes some other build. It is a
// contradiction, not silence, so it answers for nothing either.
func TestRefOccurrence_LockNamingAnotherVersionAnswersForNothing(t *testing.T) {
	l := occurrenceLock(lock.CurrentLockVersion, lock.Reference{
		From: "", Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
		Ref: "oci://ghcr.io/acme/shared-config-contract:2.1.0", Digest: refDigest("platform-settings"),
	})
	l.Root = lock.RootInfo{Version: "9.9.9"}
	snap := occurrenceApp(t, l)

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || rel.Resolved || rel.ToService != "" {
		t.Fatalf("another build's lock must not link this one: %+v", rel)
	}
	if rel.Reason == "" {
		t.Error("the unresolved verdict must say why")
	}
	if rel.LockedDigest != "" {
		t.Errorf("no pin may be carried over from another build's lock, got %q", rel.LockedDigest)
	}
}

// A ref the author pinned to a digest is a content address in its own right, so
// it still resolves through a lock that cannot answer for the occurrence. The
// legacy degrade must not swallow evidence the ref itself carries.
func TestRefOccurrence_AuthorPinnedRefSurvivesALegacyLock(t *testing.T) {
	app := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{
			{Name: "settings", Ref: "oci://ghcr.io/acme/shared-config-contract@" + refDigest("platform-settings")},
		},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: refDigest("app"),
			Lock: occurrenceLock(1, lock.Reference{
				Kind: contract.ReferenceKindConfig, Name: "settings", Source: "oci",
				Digest: refDigest("a-bundle-nobody-collected"),
			})},
		{Bundle: bundleFor(t, "platform-settings"), Digest: refDigest("platform-settings")},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "oci", col))
	if err != nil {
		t.Fatal(err)
	}

	rel := relFrom(snap.Relationships, "app", "settings")
	if rel == nil || !rel.Resolved || rel.ToService != NewServiceKey("platform-settings") {
		t.Errorf("a digest-pinned ref names one bundle whatever the lock does or does not record: %+v", rel)
	}
}
