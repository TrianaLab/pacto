package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// Adversarial coverage for REFERENCE OCCURRENCE identity in the lock closure.
//
// pacto.lock pins the TRANSITIVE config/policy closure, so it routinely holds
// several entries sharing a (kind, name): one declared by the root contract and
// one declared by a bundle the root reached through some other reference. Every
// digest in there is authoritative; each is authoritative for exactly ONE
// declared reference occurrence, and nothing but the recorded declaring contract
// says which.
//
// These tests fix the closure's half of that contract: one entry per declared
// occurrence, each tagged with the contract that declared it, and a traversal
// key that identifies the RESOLVED bundle rather than the ref text used to reach
// it.

func occIndex(refs []lock.Reference) map[lock.Occurrence]lock.Reference {
	out := make(map[lock.Occurrence]lock.Reference, len(refs))
	for _, r := range refs {
		out[r.Occurrence()] = r
	}
	return out
}

// configRefContract mirrors policyRefContract for the config kind, with an
// explicit scope name per ref so a caller can force a name collision.
func configRefContract(name, version string, scopes map[string]string) *contract.Contract {
	c := &contract.Contract{Service: contract.Service{Name: name, Version: version}}
	for _, scope := range sortedKeys(scopes) {
		c.Configurations = append(c.Configurations, contract.Configuration{Name: scope, Ref: scopes[scope]})
	}
	return c
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// collisionStore serves the counterexample topology:
//
//	app       config "foo"      -> child-a       policy "bar"        -> child-b
//	app       config "settings" -> bundle-y      policy "guardrails" -> bundle-q
//	child-a   config "settings" -> bundle-x
//	child-b   policy "guardrails" -> bundle-p
func collisionStore() *testutil.MockBundleStore {
	childA := configRefContract("child-a", "1.0.0", map[string]string{"settings": "oci://r/bundle-x"})
	childB := &contract.Contract{
		Service:  contract.Service{Name: "child-b", Version: "1.0.0"},
		Policies: []contract.Policy{{Name: "guardrails", Ref: "oci://r/bundle-p"}},
	}
	leaves := map[string]string{
		"bundle-x": "9.0.0", "bundle-y": "9.1.0", "bundle-p": "9.2.0", "bundle-q": "9.3.0",
	}
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			switch {
			case strings.Contains(ref, "child-a"):
				return &contract.Bundle{Contract: childA}, nil
			case strings.Contains(ref, "child-b"):
				return &contract.Bundle{Contract: childB}, nil
			}
			for name, version := range leaves {
				if strings.Contains(ref, name) {
					return &contract.Bundle{Contract: &contract.Contract{
						Service: contract.Service{Name: name, Version: version}}}, nil
				}
			}
			return nil, os.ErrNotExist
		},
	}
}

func collisionRoot() *contract.Contract {
	return &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "foo", Ref: "oci://r/child-a"},
			{Name: "settings", Ref: "oci://r/bundle-y"},
		},
		Policies: []contract.Policy{
			{Name: "bar", Ref: "oci://r/child-b"},
			{Name: "guardrails", Ref: "oci://r/bundle-q"},
		},
	}
}

// THE closure counterexample: a transitive entry and a direct entry may share a
// (kind, name), and the lock must record which contract declared each.
func TestReferenceClosure_SameNameAcrossRootAndChildStayDistinct(t *testing.T) {
	s := NewService(collisionStore(), nil)
	refs, err := s.buildReferenceClosure(context.Background(), collisionRoot(), "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 6 {
		t.Fatalf("want one entry per declared occurrence (6), got %d: %+v", len(refs), refs)
	}
	byOcc := occIndex(refs)
	// The declaring identity of child-a's and child-b's own scopes is whatever the
	// root's entries recorded as their destination -- read off the closure rather
	// than spelled out, because that IS the invariant.
	childA := byOcc[lock.Occurrence{Kind: "config", Name: "foo"}].DestinationID()
	childB := byOcc[lock.Occurrence{Kind: "policy", Name: "bar"}].DestinationID()
	if childA == "" || childB == "" || childA == childB {
		t.Fatalf("the root's two referenced bundles must have distinct identities: %q %q", childA, childB)
	}
	for _, tc := range []struct{ from, kind, name, wantRef string }{
		{"", "config", "foo", "oci://r/child-a"},
		{"", "config", "settings", "oci://r/bundle-y"},
		{childA, "config", "settings", "oci://r/bundle-x"},
		{"", "policy", "bar", "oci://r/child-b"},
		{"", "policy", "guardrails", "oci://r/bundle-q"},
		{childB, "policy", "guardrails", "oci://r/bundle-p"},
	} {
		got, ok := byOcc[lock.Occurrence{From: tc.from, Kind: tc.kind, Name: tc.name}]
		if !ok {
			t.Errorf("occurrence %q %s/%s missing from the closure", tc.from, tc.kind, tc.name)
			continue
		}
		if got.Ref != tc.wantRef {
			t.Errorf("occurrence %q %s/%s pinned %q, want %q", tc.from, tc.kind, tc.name, got.Ref, tc.wantRef)
		}
		if got.Digest == "" {
			t.Errorf("occurrence %q %s/%s is not pinned", tc.from, tc.kind, tc.name)
		}
	}
}

// A lock built from the counterexample answers the direct lookup with the entry
// the ROOT declared, never the transitive namesake.
func TestLockRootReference_IgnoresTransitiveNamesakes(t *testing.T) {
	s := NewService(collisionStore(), nil)
	root := collisionRoot()
	l, err := s.buildLock(context.Background(), "oci://r/app:1.0.0", &contract.Bundle{Contract: root}, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	// Round trip through the serialized form: sorted order is as arbitrary with
	// respect to the declaring contract as slice order was.
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lock.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, tc := range []struct{ kind, name, wantRef string }{
		{"config", "settings", "oci://r/bundle-y"},
		{"policy", "guardrails", "oci://r/bundle-q"},
	} {
		got, ok := parsed.RootReference(tc.kind, tc.name)
		if !ok {
			t.Fatalf("root %s/%s not found", tc.kind, tc.name)
		}
		if got.Ref != tc.wantRef {
			t.Errorf("root %s/%s resolved to %q, want %q", tc.kind, tc.name, got.Ref, tc.wantRef)
		}
	}
}

// writeBundle writes a minimal bundle at dir with the given trailing YAML.
func writeBundle(t *testing.T, dir, name, version, extra string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	y := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"" + version + "\"\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
}

// localCollisionTree lays out the relative-path counterexample:
//
//	<root>/pacto.yaml         config "child"  -> ../child   config "shared" -> ./config
//	<root>/config/pacto.yaml  service root-config
//	<child>/pacto.yaml        config "shared" -> ./config
//	<child>/config/pacto.yaml service child-config
//
// Both "shared" occurrences are the literal text "./config". They denote
// different directories, because a relative ref is resolved against the
// directory of the contract that DECLARED it. Deduplicating the walk by ref text
// therefore collapses two different resources into one -- and, depending on
// which is walked first, either drops a real closure member from the lock or
// pins the wrong bundle under the other's name.
func localCollisionTree(t *testing.T) (rootDir string) {
	t.Helper()
	base := t.TempDir()
	rootDir = filepath.Join(base, "root")
	childDir := filepath.Join(base, "child")
	writeBundle(t, rootDir, "root", "1.0.0",
		"configurations:\n  - name: child\n    ref: ../child\n  - name: shared\n    ref: ./config\n")
	writeBundle(t, filepath.Join(rootDir, "config"), "root-config", "1.0.0", "")
	writeBundle(t, childDir, "child", "1.0.0",
		"configurations:\n  - name: shared\n    ref: ./config\n")
	writeBundle(t, filepath.Join(childDir, "config"), "child-config", "2.0.0", "")
	return rootDir
}

// The same relative ref text declared from two directories names two different
// bundles, and the closure must pin both.
func TestReferenceClosure_SameRelativeRefFromDifferentDirectories(t *testing.T) {
	rootDir := localCollisionTree(t)
	s := NewService(&testutil.MockBundleStore{}, nil)
	root, err := loadLocalBundle(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.buildReferenceClosure(context.Background(), root.Contract, rootDir)
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 occurrences (root/child, root/shared, child/shared), got %d: %+v", len(refs), refs)
	}
	byOcc := occIndex(refs)
	rootShared, ok := byOcc[lock.Occurrence{Kind: "config", Name: "shared"}]
	if !ok {
		t.Fatal("the root's own ./config occurrence is missing from the closure")
	}
	childID := byOcc[lock.Occurrence{Kind: "config", Name: "child"}].DestinationID()
	childShared, ok := byOcc[lock.Occurrence{From: childID, Kind: "config", Name: "shared"}]
	if !ok {
		t.Fatalf("the child's ./config occurrence is missing from the closure (declarer %q)", childID)
	}
	if rootShared.Version != "1.0.0" {
		t.Errorf("root's ./config pinned version %q, want root-config's 1.0.0", rootShared.Version)
	}
	if childShared.Version != "2.0.0" {
		t.Errorf("child's ./config pinned version %q, want child-config's 2.0.0", childShared.Version)
	}
	if rootShared.ContentHash == childShared.ContentHash {
		t.Errorf("two different bundles pinned to one content hash %q", rootShared.ContentHash)
	}
}

// Two occurrences that really do resolve to the same immutable bundle stay two
// occurrences, each pinned, agreeing on the identity. Distinguishing occurrences
// must not fabricate a difference where there is none.
func TestReferenceClosure_SameTargetTwiceIsTwoAgreeingOccurrences(t *testing.T) {
	leaf := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "leaf", Version: "4.0.0"}}}
	s := NewService(&testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn:    func(_ context.Context, _ string) (*contract.Bundle, error) { return leaf, nil },
	}, nil)
	root := &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "a", Ref: "oci://r/leaf:1.0.0"},
			{Name: "b", Ref: "oci://r/leaf:1.0.0"},
		},
	}
	refs, err := s.buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want one entry per declaration (2), got %d: %+v", len(refs), refs)
	}
	if refs[0].Name == refs[1].Name {
		t.Errorf("two scopes sharing a ref string are still two declarations: %+v", refs)
	}
	if refs[0].Digest == "" || refs[0].Digest != refs[1].Digest {
		t.Errorf("both declarations resolve the same bundle and must agree: %+v", refs)
	}
}

// A cycle terminates on the RESOLVED bundle, not on the ref text used to reach
// it, and each declared occurrence around the cycle is still pinned once.
func TestReferenceClosure_CycleTerminatesOnResolvedIdentity(t *testing.T) {
	p := configRefContract("p", "1.0.0", map[string]string{"to-q": "oci://r/q"})
	q := configRefContract("q", "1.0.0", map[string]string{"to-p": "oci://r/p"})
	s := NewService(&testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "/p") {
				return &contract.Bundle{Contract: p}, nil
			}
			return &contract.Bundle{Contract: q}, nil
		},
	}, nil)
	root := configRefContract("root", "0.1.0", map[string]string{"to-p": "oci://r/p"})
	refs, err := s.buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("cycle should terminate: %v", err)
	}
	// root/to-p, p's to-q, q's to-p. q's to-p resolves p again, which is already
	// walked, so the walk stops there.
	if len(refs) != 3 {
		t.Fatalf("want 3 occurrences around the cycle, got %d: %+v", len(refs), refs)
	}
	byOcc := occIndex(refs)
	toQ := byOcc[lock.Occurrence{From: byOcc[lock.Occurrence{Kind: "config", Name: "to-p"}].DestinationID(),
		Kind: "config", Name: "to-q"}]
	if _, ok := byOcc[lock.Occurrence{From: toQ.DestinationID(), Kind: "config", Name: "to-p"}]; !ok {
		t.Errorf("the occurrence that closes the cycle must be declared by q: %+v", refs)
	}
}

// Regenerating the lock from an unchanged tree produces byte-identical output.
func TestBuildLock_DeterministicRegeneration(t *testing.T) {
	root := &contract.Bundle{Contract: collisionRoot()}
	var first []byte
	for i := 0; i < 3; i++ {
		s := NewService(collisionStore(), nil)
		l, err := s.buildLock(context.Background(), "oci://r/app:1.0.0", root, nil)
		if err != nil {
			t.Fatalf("buildLock: %v", err)
		}
		data, err := l.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatalf("lock regeneration is not deterministic:\n--- first ---\n%s\n--- run %d ---\n%s", first, i, data)
		}
	}
}
