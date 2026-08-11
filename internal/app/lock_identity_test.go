package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// Counterexamples for the IDENTITY of a reference occurrence in pacto.lock.
//
// The lock claims to identify every transitive reference occurrence in the
// closure. That claim is only true if the serialized identity is injective over
// every contract the schema accepts. Configuration and policy names are
// `{"type":"string","minLength":1}` — no pattern, no charset restriction — so
// "a/policy:b" is as legal a scope name as "settings", and any identity built by
// joining names with delimiters is a claim the schema does not support.
//
// These tests are written against the MODEL, not against one encoding: they
// assert that the occurrence identity determines the resolution, that it does
// not depend on traversal order, and that an occurrence the lock cannot
// represent is refused rather than silently written.

// refStore serves one bundle per OCI ref, each with its own content identity.
// Bundle names must not be substrings of one another.
func refStore(bundles map[string]*contract.Contract) *testutil.MockBundleStore {
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			for name, c := range bundles {
				if strings.Contains(ref, name) {
					return &contract.Bundle{Contract: c}, nil
				}
			}
			return nil, os.ErrNotExist
		},
	}
}

// cfg builds a contract declaring configuration scopes in the given order.
func cfg(name string, scopes ...[2]string) *contract.Contract {
	c := &contract.Contract{Service: contract.Service{Name: name, Version: "1.0.0"}}
	for _, s := range scopes {
		c.Configurations = append(c.Configurations, contract.Configuration{Name: s[0], Ref: s[1]})
	}
	return c
}

// occurrences indexes a closure by occurrence identity, failing the test on the
// first identity that names two different resolutions. This is the property
// under test: the identity must be a key.
func occurrences(t *testing.T, refs []lock.Reference) map[lock.Occurrence]lock.Reference {
	t.Helper()
	out := make(map[lock.Occurrence]lock.Reference, len(refs))
	for _, r := range refs {
		occ := r.Occurrence()
		if prev, dup := out[occ]; dup && prev != r {
			t.Fatalf("occurrence %s names two different resolutions:\n  %+v\n  %+v", occ, prev, r)
		}
		out[occ] = r
	}
	return out
}

// THE delimiter counterexample.
//
//	root  config "a/policy:b" -> alpha        alpha   config "settings" -> mike
//	root  config "a"          -> charlie      charlie policy "b"        -> bravo
//	                                          bravo   config "settings" -> november
//
// Under a path identity of parent + "/" + kind + ":" + name, the root's own
// scope named "a/policy:b" produces the path "config:a/policy:b", and so does
// the policy "b" declared by the bundle reached through the scope named "a".
// Two different declarations, in two different contracts, resolving to two
// different bundles, then both declare a scope called "settings" — and the lock
// files both under ("config:a/policy:b", config, settings). Whichever is read
// back names a bundle the other contract never referenced.
func TestReferenceOccurrence_DelimiterCollisionIsNotAnIdentity(t *testing.T) {
	store := refStore(map[string]*contract.Contract{
		"alpha":    cfg("alpha", [2]string{"settings", "oci://r/mike"}),
		"bravo":    cfg("bravo", [2]string{"settings", "oci://r/november"}),
		"charlie":  {Service: contract.Service{Name: "charlie", Version: "1.0.0"}, Policies: []contract.Policy{{Name: "b", Ref: "oci://r/bravo"}}},
		"mike":     cfg("mike"),
		"november": cfg("november"),
	})
	root := cfg("app",
		[2]string{"a/policy:b", "oci://r/alpha"},
		[2]string{"a", "oci://r/charlie"},
	)

	refs, err := NewService(store, nil).buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 5 {
		t.Fatalf("want one entry per declaration (5), got %d: %+v", len(refs), refs)
	}

	// Round-trip: the serialized lock is what a later reader actually has.
	l := &lock.Lock{LockVersion: lock.CurrentLockVersion, Root: lock.RootInfo{Name: "app", Version: "1.0.0"}, References: refs}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lock.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	byOcc := occurrences(t, parsed.References)
	if len(byOcc) != len(parsed.References) {
		t.Fatalf("%d entries collapsed into %d occurrence identities:\n%s", len(parsed.References), len(byOcc), data)
	}

	// The two "settings" scopes are declared by two different contracts and must
	// stay tellable apart by their identity alone.
	var settings []lock.Reference
	for _, r := range parsed.References {
		if r.Kind == "config" && r.Name == "settings" {
			settings = append(settings, r)
		}
	}
	if len(settings) != 2 {
		t.Fatalf("want both declared \"settings\" scopes, got %d: %+v", len(settings), settings)
	}
	if settings[0].From == settings[1].From {
		t.Errorf("two contracts' \"settings\" scopes share the declaring identity %q", settings[0].From)
	}
	if settings[0].Ref == settings[1].Ref {
		t.Errorf("both \"settings\" scopes pinned the same bundle %q", settings[0].Ref)
	}
}

// The declaring identity is never "" except for the root, so "root declared it"
// stays a decidable question no matter what a name contains.
func TestReferenceOccurrence_EmptyFromMeansRootOnly(t *testing.T) {
	store := refStore(map[string]*contract.Contract{
		"alpha":    cfg("alpha", [2]string{"", "oci://r/mike"}, [2]string{"/", "oci://r/november"}),
		"mike":     cfg("mike"),
		"november": cfg("november"),
	})
	// A scope with an empty name is not schema-legal, but the closure builder is
	// reached by `pacto lock`, which does not validate. It must still not be able
	// to forge a root entry.
	root := cfg("app", [2]string{"alpha", "oci://r/alpha"})
	refs, err := NewService(store, nil).buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	var rootDeclared int
	for _, r := range refs {
		if r.From == "" {
			rootDeclared++
			if r.Name != "alpha" {
				t.Errorf("entry %+v claims the root declared it", r)
			}
		}
	}
	if rootDeclared != 1 {
		t.Errorf("want exactly the root's own declaration with an empty From, got %d", rootDeclared)
	}
}

// Names that are hostile to any string encoding must still round-trip to
// distinct occurrences.
func TestReferenceOccurrence_HostileNamesStayDistinct(t *testing.T) {
	hostile := []string{
		"a/policy:b",
		"a", // the prefix of the first, so a prefix-based split cannot work either
		"config:a",
		"nul\x00byte",
		"line\nbreak",
		"quote\"and'apostrophe",
		"emoji-\U0001f512",
		"rtl-\u202egnp.exe",
		strings.Repeat("deep/", 40) + "leaf",
	}
	bundles := map[string]*contract.Contract{}
	var scopes [][2]string
	for i, name := range hostile {
		bundle := "leaf" + string(rune('A'+i))
		bundles[bundle] = cfg(bundle)
		scopes = append(scopes, [2]string{name, "oci://r/" + bundle})
	}
	root := cfg("app", scopes...)

	refs, err := NewService(refStore(bundles), nil).buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	l := &lock.Lock{LockVersion: lock.CurrentLockVersion, Root: lock.RootInfo{Name: "app", Version: "1.0.0"}, References: refs}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lock.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	byOcc := occurrences(t, parsed.References)
	if len(byOcc) != len(hostile) {
		t.Fatalf("want %d distinct occurrences, got %d:\n%s", len(hostile), len(byOcc), data)
	}
	for _, name := range hostile {
		if _, ok := byOcc[lock.Occurrence{Kind: "config", Name: name}]; !ok {
			t.Errorf("scope %q lost its identity through the lock", name)
		}
	}
}

// diamondRoot: two of the root's scopes reach the SAME bundle, which declares a
// scope of its own. The grandchild declaration exists once, inside one immutable
// contract; which of the two paths the walk happened to take first is not part
// of its identity.
func diamondStore() *testutil.MockBundleStore {
	return refStore(map[string]*contract.Contract{
		"mid":  cfg("mid", [2]string{"deep", "oci://r/leaf"}),
		"leaf": cfg("leaf"),
	})
}

func TestReferenceOccurrence_MultiplePathsToOneContractAreOneDeclaration(t *testing.T) {
	root := cfg("app",
		[2]string{"left", "oci://r/mid"},
		[2]string{"right", "oci://r/mid"},
	)
	refs, err := NewService(diamondStore(), nil).buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	// Two root declarations plus mid's one declaration. "deep" is declared once,
	// in one contract, so it is one entry however many paths reach it.
	if len(refs) != 3 {
		t.Fatalf("want 3 declarations (left, right, mid's deep), got %d: %+v", len(refs), refs)
	}
	byOcc := occurrences(t, refs)
	left := byOcc[lock.Occurrence{Kind: "config", Name: "left"}]
	right := byOcc[lock.Occurrence{Kind: "config", Name: "right"}]
	if left.DestinationID() == "" || left.DestinationID() != right.DestinationID() {
		t.Fatalf("both root scopes reach one bundle and must record one destination: %+v %+v", left, right)
	}
	// Provenance is derivable: the grandchild's declaring identity IS the
	// destination the two root scopes recorded, so BOTH paths are readable off
	// the entry set without either being privileged.
	var deep lock.Reference
	for _, r := range refs {
		if r.Name == "deep" {
			deep = r
		}
	}
	if deep.From != left.DestinationID() {
		t.Errorf("deep is declared by %q, want the bundle both root scopes reached (%q)", deep.From, left.DestinationID())
	}
}

// The same closure, declared in the other order, must produce the same lock.
func TestReferenceOccurrence_DeclarationOrderDoesNotChangeTheLock(t *testing.T) {
	marshal := func(root *contract.Contract) []byte {
		t.Helper()
		refs, err := NewService(diamondStore(), nil).buildReferenceClosure(context.Background(), root, "")
		if err != nil {
			t.Fatalf("buildReferenceClosure: %v", err)
		}
		l := &lock.Lock{LockVersion: lock.CurrentLockVersion, Root: lock.RootInfo{Name: "app", Version: "1.0.0"}, References: refs}
		data, err := l.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	a := marshal(cfg("app", [2]string{"left", "oci://r/mid"}, [2]string{"right", "oci://r/mid"}))
	b := marshal(cfg("app", [2]string{"right", "oci://r/mid"}, [2]string{"left", "oci://r/mid"}))
	if !bytes.Equal(a, b) {
		t.Errorf("declaration order changed the lock:\n--- left first ---\n%s\n--- right first ---\n%s", a, b)
	}
}

// Duplicate declaration names are rejected before any of this, by the
// declaration-uniqueness gate at the top of the walk, whether or not the two
// declarations resolve alike -- see lock_duplicate_test.go. What remains here is
// the case that only a RESOLUTION can reveal.

// Two byte-identical local bundle directories, each resolving the same relative
// ref to a different sibling:
//
//	root/pacto.yaml      config "a" -> ../one/twin   config "b" -> ../two/twin
//	one/twin/pacto.yaml  config "shared" -> ../cfg     one/cfg  service one-cfg
//	two/twin/pacto.yaml  config "shared" -> ../cfg     two/cfg  service two-cfg
//
// one/twin and two/twin are the SAME contract: identical bytes, therefore one
// content identity. A declaration lives inside a contract, so "shared" is one
// declaration -- and it resolves to one/cfg or two/cfg depending on where that
// contract happens to sit on disk, which is not part of the contract and so
// cannot be part of the lock. This is genuinely outside what the ontology can
// express, and the only honest answer is to refuse rather than pin whichever
// directory the walk saw first. Note that this is not the duplicate-name case:
// canonical validation accepts every contract here.
func TestReferenceClosure_IdenticalLocalTwinsResolvingDifferentlyFailClosed(t *testing.T) {
	base := t.TempDir()
	twin := "configurations:\n  - name: shared\n    ref: ../cfg\n"
	for _, side := range []string{"one", "two"} {
		writeBundle(t, filepath.Join(base, side, "twin"), "twin", "1.0.0", twin)
		writeBundle(t, filepath.Join(base, side, "cfg"), side+"-cfg", "1.0.0", "")
	}
	rootDir := filepath.Join(base, "root")
	writeBundle(t, rootDir, "app", "1.0.0",
		"configurations:\n  - name: a\n    ref: ../one/twin\n  - name: b\n    ref: ../two/twin\n")

	root, err := loadLocalBundle(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewService(nil, nil).buildReferenceClosure(context.Background(), root.Contract, rootDir)
	var amb *lock.AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("want *lock.AmbiguousError, got %v", err)
	}
	if amb.Occurrence.Name != "shared" || amb.Occurrence.From == "" {
		t.Errorf("the error must name the twin's own declaration, got %+v", amb.Occurrence)
	}
	// Both resolutions are shown, and the declared text alone does not tell them
	// apart -- which is the whole reason the lock cannot hold them both.
	if !strings.Contains(amb.Error(), "../cfg") || amb.First == amb.Second {
		t.Errorf("the error must show both resolutions of ../cfg: %v", amb)
	}
}

// The agreeing half of the twin case: identical twins whose siblings are also
// identical. The walk still enters both directories -- they are two places on
// disk -- but the declaration it finds there is one declaration with one
// resolution, so it is emitted once and where the copies sit does not leak into
// the lock.
func TestReferenceClosure_IdenticalLocalTwinsAgreeingAreOneDeclaration(t *testing.T) {
	base := t.TempDir()
	twin := "configurations:\n  - name: shared\n    ref: ../cfg\n"
	for _, side := range []string{"one", "two"} {
		writeBundle(t, filepath.Join(base, side, "twin"), "twin", "1.0.0", twin)
		writeBundle(t, filepath.Join(base, side, "cfg"), "shared-cfg", "1.0.0", "")
	}
	rootDir := filepath.Join(base, "root")
	writeBundle(t, rootDir, "app", "1.0.0",
		"configurations:\n  - name: a\n    ref: ../one/twin\n  - name: b\n    ref: ../two/twin\n")

	root, err := loadLocalBundle(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := NewService(nil, nil).buildReferenceClosure(context.Background(), root.Contract, rootDir)
	if err != nil {
		t.Fatalf("twins that agree are not an ambiguity: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want the root's two scopes plus one \"shared\", got %d: %+v", len(refs), refs)
	}
	occurrences(t, refs) // the identity must still be a key
}

// The same declaration reached twice is one entry, not an ambiguity.
func TestReferenceClosure_IdenticalDeclarationReachedTwiceIsNotAmbiguous(t *testing.T) {
	root := cfg("app",
		[2]string{"left", "oci://r/mid"},
		[2]string{"right", "oci://r/mid"},
	)
	if _, err := NewService(diamondStore(), nil).buildReferenceClosure(context.Background(), root, ""); err != nil {
		t.Fatalf("one bundle reached twice is not an ambiguity: %v", err)
	}
}
