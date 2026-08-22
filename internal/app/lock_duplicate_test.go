package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// Adversarial coverage for the OTHER half of reference occurrence identity: a
// lock entry is identified by (declaring contract, kind, name), so a contract
// that declares one name twice has two declarations the lock cannot tell apart.
//
// Canonical cross-field validation already rejects duplicate configuration and
// policy names, but `pacto lock` resolves the closure WITHOUT validating it, so
// the closure walk is the last gate. It used to fail only when the two
// declarations resolved differently; two duplicates that happened to resolve to
// identical bytes were silently collapsed into one entry, which contradicts the
// one-entry-per-declaration claim the lock format makes.

// dupStore serves every ref as its own pinned leaf, so a duplicate name is the
// only thing under test.
func dupStore() *testutil.MockBundleStore {
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			name := ref[strings.LastIndex(ref, "/")+1:]
			return &contract.Bundle{Contract: &contract.Contract{
				Service: contract.Service{Name: name, Version: "9.0.0"}}}, nil
		},
	}
}

// wantDuplicateRejected asserts the closure refuses the contract and names the
// declaration it refused, rather than quietly emitting fewer entries than the
// contract declared.
func wantDuplicateRejected(t *testing.T, root *contract.Contract, kind, name string) {
	t.Helper()
	s := NewService(dupStore(), nil)
	refs, err := s.buildReferenceClosure(context.Background(), root, "")
	if err == nil {
		t.Fatalf("a contract declaring %s %q twice was locked as %d entries; a lock cannot tell the two apart", kind, name, len(refs))
	}
	var dup *lock.DuplicateDeclarationError
	if !errors.As(err, &dup) {
		t.Fatalf("want *lock.DuplicateDeclarationError, got %T: %v", err, err)
	}
	if dup.Occurrence.Kind != kind || dup.Occurrence.Name != name {
		t.Errorf("error names %s %q, want %s %q", dup.Occurrence.Kind, dup.Occurrence.Name, kind, name)
	}
	// The message has to say which declaration to go and fix; a code alone sends
	// the reader back to a contract to find the repeat themselves.
	if msg := dup.Error(); !strings.Contains(msg, kind) || !strings.Contains(msg, name) {
		t.Errorf("the message must name the repeated declaration: %s", msg)
	}
}

// Identical bytes are the hard case: nothing about the RESOLUTION distinguishes
// the two declarations, so a builder that only compares resolutions sees one.
func TestReferenceClosure_DuplicateConfigurationSameRef(t *testing.T) {
	wantDuplicateRejected(t, &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "settings", Ref: "oci://r/leaf"},
			{Name: "settings", Ref: "oci://r/leaf"},
		},
	}, contract.ReferenceKindConfig, "settings")
}

func TestReferenceClosure_DuplicateConfigurationDifferentRef(t *testing.T) {
	wantDuplicateRejected(t, &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "settings", Ref: "oci://r/leaf-a"},
			{Name: "settings", Ref: "oci://r/leaf-b"},
		},
	}, contract.ReferenceKindConfig, "settings")
}

func TestReferenceClosure_DuplicatePolicySameRef(t *testing.T) {
	wantDuplicateRejected(t, &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Policies: []contract.Policy{
			{Name: "guardrails", Ref: "oci://r/leaf"},
			{Name: "guardrails", Ref: "oci://r/leaf"},
		},
	}, contract.ReferenceKindPolicy, "guardrails")
}

func TestReferenceClosure_DuplicatePolicyDifferentRef(t *testing.T) {
	wantDuplicateRejected(t, &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Policies: []contract.Policy{
			{Name: "guardrails", Ref: "oci://r/leaf-a"},
			{Name: "guardrails", Ref: "oci://r/leaf-b"},
		},
	}, contract.ReferenceKindPolicy, "guardrails")
}

// A declaration carrying an inline schema is invisible to ReferenceRefs, so a
// gate built only from the refs the walk resolves never sees this pair -- and
// the surviving ref entry would be filed under a name the contract uses twice.
func TestReferenceClosure_DuplicateConfigurationOnlyOneOfWhichIsARef(t *testing.T) {
	wantDuplicateRejected(t, &contract.Contract{
		Service: contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "settings", Schema: "configuration/schema.json"},
			{Name: "settings", Ref: "oci://r/leaf"},
		},
	}, contract.ReferenceKindConfig, "settings")
}

// The rule is a property of every contract in the closure, not of the root: a
// referenced bundle declaring one name twice is just as unlockable.
func TestReferenceClosure_DuplicateInsideAReferencedContract(t *testing.T) {
	child := &contract.Contract{
		Service: contract.Service{Name: "child", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "settings", Ref: "oci://r/leaf"},
			{Name: "settings", Ref: "oci://r/leaf"},
		},
	}
	s := NewService(&testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "child") {
				return &contract.Bundle{Contract: child}, nil
			}
			return &contract.Bundle{Contract: &contract.Contract{
				Service: contract.Service{Name: "leaf", Version: "9.0.0"}}}, nil
		},
	}, nil)
	root := configRefContract("app", "1.0.0", map[string]string{"to-child": "oci://r/child"})

	_, err := s.buildReferenceClosure(context.Background(), root, "")
	var dup *lock.DuplicateDeclarationError
	if !errors.As(err, &dup) {
		t.Fatalf("want *lock.DuplicateDeclarationError, got %T: %v", err, err)
	}
	if dup.Occurrence.From == "" {
		t.Errorf("the error blames the root contract for the child's duplicate: %v", dup)
	}
}

// The rule is per kind. A configuration and a policy sharing a name are two
// different lookups, and locking them must keep working.
func TestReferenceClosure_ConfigAndPolicyMaySharedAName(t *testing.T) {
	s := NewService(dupStore(), nil)
	refs, err := s.buildReferenceClosure(context.Background(), &contract.Contract{
		Service:        contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{{Name: "shared", Ref: "oci://r/leaf-a"}},
		Policies:       []contract.Policy{{Name: "shared", Ref: "oci://r/leaf-b"}},
	}, "")
	if err != nil {
		t.Fatalf("a config and a policy named alike are not duplicates: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want both declarations pinned, got %d: %+v", len(refs), refs)
	}
}
