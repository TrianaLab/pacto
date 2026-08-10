package lock

import (
	"strings"
	"testing"
)

func sample() *Lock {
	return &Lock{
		LockVersion: CurrentLockVersion,
		Pacto:       PactoInfo{Version: "1.4.0"},
		Root:        RootInfo{Name: "payments-api", Version: "2.1.0"},
		Dependencies: []Entry{
			{Name: "zeta", Source: "oci", Ref: "oci://r/zeta", Constraint: "^1.0.0", Version: "1.2.0", Digest: "sha256:z", DependsOn: []string{"beta", "alpha"}},
			{Name: "alpha", Source: "oci", Ref: "oci://r/alpha", Constraint: "^2.0.0", Version: "2.5.1", Digest: "sha256:a"},
			{Name: "local-lib", Source: "local", Path: "../local-lib", ContentHash: "sha256:l"},
		},
		References: []Reference{
			{Kind: "policy", Name: "sec", Source: "oci", Ref: "oci://r/sec", Version: "1.3.0", Digest: "sha256:s"},
			{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/cfg", Version: "2.0.0", Digest: "sha256:c"},
		},
	}
}

func TestMarshalDeterministicAndSorted(t *testing.T) {
	data, err := sample().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)
	// dependencies sorted by name: alpha before local-lib before zeta.
	ia, il, iz := strings.Index(out, "name: alpha"), strings.Index(out, "name: local-lib"), strings.Index(out, "name: zeta")
	if ia >= il || il >= iz {
		t.Errorf("dependencies not sorted by name:\n%s", out)
	}
	// dependsOn sorted: alpha before beta.
	deps := out[strings.Index(out, "name: zeta"):]
	if strings.Index(deps, "alpha") > strings.Index(deps, "beta") {
		t.Errorf("dependsOn not sorted:\n%s", out)
	}
	// re-marshal is byte-identical.
	data2, _ := sample().Marshal()
	if string(data2) != out {
		t.Errorf("marshal not deterministic")
	}
}

// TestReferencesSortDeterministicOnRefTiebreak proves the (Kind, Name, Ref, Path)
// total order: two references sharing Kind+Name but differing by Ref keep a stable,
// byte-identical order across repeated Marshal calls. Keying only on (Kind, Name)
// left these reorderable under sort.Slice (unstable), breaking the relock guarantee.
func TestReferencesSortDeterministicOnRefTiebreak(t *testing.T) {
	mk := func() *Lock {
		return &Lock{
			LockVersion: CurrentLockVersion,
			Pacto:       PactoInfo{Version: "1.4.0"},
			Root:        RootInfo{Name: "test", Version: "1.0.0"},
			References: []Reference{
				{Kind: "policy", Name: "sec", Source: "oci", Ref: "oci://r/sec-b", Digest: "sha256:b"},
				{Kind: "policy", Name: "sec", Source: "oci", Ref: "oci://r/sec-a", Digest: "sha256:a"},
			},
		}
	}
	data, err := mk().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)
	// Ref tiebreak: sec-a sorts before sec-b regardless of input order.
	if strings.Index(out, "sec-a") > strings.Index(out, "sec-b") {
		t.Errorf("references not sorted by ref tiebreak:\n%s", out)
	}
	// Re-marshal is byte-identical across runs.
	for i := 0; i < 5; i++ {
		data2, err := mk().Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(data2) != out {
			t.Fatalf("marshal not deterministic on ref tiebreak (iter %d)", i)
		}
	}

	// Final Path tiebreak: same Kind+Name+Ref, differing only by local Path.
	pl := &Lock{
		LockVersion: CurrentLockVersion,
		Pacto:       PactoInfo{Version: "1.4.0"},
		Root:        RootInfo{Name: "test", Version: "1.0.0"},
		References: []Reference{
			{Kind: "config", Name: "cfg", Source: "local", Path: "../z-cfg"},
			{Kind: "config", Name: "cfg", Source: "local", Path: "../a-cfg"},
		},
	}
	pData, err := pl.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	pOut := string(pData)
	if strings.Index(pOut, "a-cfg") > strings.Index(pOut, "z-cfg") {
		t.Errorf("references not sorted by path tiebreak:\n%s", pOut)
	}
}

func TestParseRoundTrip(t *testing.T) {
	data, _ := sample().Marshal()
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Root.Name != "payments-api" || len(got.Dependencies) != 3 || len(got.References) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if e, ok := got.Dependency("alpha"); !ok || e.Digest != "sha256:a" {
		t.Errorf("Dependency(alpha) lookup failed: %+v %v", e, ok)
	}
	if _, ok := got.Dependency("missing"); ok {
		t.Errorf("Dependency(missing) should be false")
	}
	if r, ok := got.RootReference("policy", "sec"); !ok || r.Digest != "sha256:s" {
		t.Errorf("Reference(policy,sec) lookup failed")
	}
	if _, ok := got.RootReference("config", "missing"); ok {
		t.Errorf("Reference(config,missing) should be false")
	}
}

func TestParseRejectsUnknownVersion(t *testing.T) {
	if _, err := Parse([]byte("lockVersion: 999\n")); err == nil {
		t.Errorf("expected error for unsupported lockVersion")
	}
}

func TestParseInvalidYAML(t *testing.T) {
	if _, err := Parse([]byte("\tnot: yaml: [")); err == nil {
		t.Errorf("expected parse error")
	}
}

func TestReferencesSortedByKindThenName(t *testing.T) {
	lock := &Lock{
		LockVersion: CurrentLockVersion,
		Pacto:       PactoInfo{Version: "1.4.0"},
		Root:        RootInfo{Name: "test", Version: "1.0.0"},
		References: []Reference{
			{Kind: "policy", Name: "zeta", Source: "oci"},
			{Kind: "config", Name: "beta", Source: "oci"},
			{Kind: "policy", Name: "alpha", Source: "oci"},
			{Kind: "config", Name: "gamma", Source: "oci"},
		},
	}
	data, err := lock.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)
	// References sorted by kind first (config before policy), then name.
	idxConfigBeta := strings.Index(out, "name: beta")
	idxConfigGamma := strings.Index(out, "name: gamma")
	idxPolicyAlpha := strings.Index(out, "name: alpha")
	idxPolicyZeta := strings.Index(out, "name: zeta")
	// config beta < config gamma < policy alpha < policy zeta
	if idxConfigBeta >= idxConfigGamma ||
		idxConfigGamma >= idxPolicyAlpha ||
		idxPolicyAlpha >= idxPolicyZeta {
		t.Errorf("references not sorted by kind then name:\n%s", out)
	}
}

func TestReferencePath(t *testing.T) {
	if got := ReferencePath("", "config", "settings"); got != "config:settings" {
		t.Errorf("root occurrence path = %q, want config:settings", got)
	}
	if got := ReferencePath("config:foo", "config", "settings"); got != "config:foo/config:settings" {
		t.Errorf("nested occurrence path = %q, want config:foo/config:settings", got)
	}
	// Distinctness is the whole point: same kind and name, different declaring
	// contract, different path.
	if ReferencePath("", "config", "settings") == ReferencePath("config:foo", "config", "settings") {
		t.Error("a root occurrence and a transitive namesake must not share a path")
	}
}

// The declaring contract leads the reference sort, so a bundle's own references
// stay grouped under it and the ordering itself never has to be interpreted.
func TestReferencesSortedByDeclaringContractFirst(t *testing.T) {
	l := &Lock{
		LockVersion: CurrentLockVersion,
		References: []Reference{
			{From: "config:foo", Kind: "config", Name: "aaa", Source: "oci", Ref: "oci://r/nested"},
			{From: "", Kind: "config", Name: "zzz", Source: "oci", Ref: "oci://r/root"},
		},
	}
	data, err := l.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)
	// Root ("") sorts ahead of "config:foo" even though its name sorts last.
	if strings.Index(out, "oci://r/root") > strings.Index(out, "oci://r/nested") {
		t.Errorf("references not sorted by declaring contract first:\n%s", out)
	}
}

// RootReference answers only for the root contract's OWN declared references.
func TestRootReferenceIgnoresEverythingItCannotAttribute(t *testing.T) {
	mine := Reference{From: "", Kind: "config", Name: "settings", Source: "oci", Digest: "sha256:mine"}
	theirs := Reference{From: "config:foo", Kind: "config", Name: "settings", Source: "oci", Digest: "sha256:theirs"}

	l := &Lock{LockVersion: CurrentLockVersion, References: []Reference{theirs, mine}}
	r, ok := l.RootReference("config", "settings")
	if !ok || r.Digest != "sha256:mine" {
		t.Errorf("RootReference returned %+v (%v), want the root's own entry", r, ok)
	}

	// A transitive namesake alone answers for nothing.
	only := &Lock{LockVersion: CurrentLockVersion, References: []Reference{theirs}}
	if _, ok := only.RootReference("config", "settings"); ok {
		t.Error("a transitive namesake must not answer for the root's reference")
	}

	// Two entries claiming the same occurrence contradict each other.
	dup := &Lock{LockVersion: CurrentLockVersion, References: []Reference{mine, mine}}
	if _, ok := dup.RootReference("config", "settings"); ok {
		t.Error("contradictory occurrence entries must not resolve")
	}

	// A pre-occurrence lock records nothing that can be attributed at all.
	legacy := &Lock{LockVersion: OccurrenceLockVersion - 1, References: []Reference{
		{Kind: "config", Name: "settings", Source: "oci", Digest: "sha256:mine"},
	}}
	if _, ok := legacy.RootReference("config", "settings"); ok {
		t.Error("a lock predating occurrence identity must not resolve a reference")
	}
}

// A lockVersion 1 file still parses, and keeps declaring 1 -- reading it as if it
// were current would silently reinterpret it under semantics it never recorded.
func TestParseAcceptsOlderSchemaWithoutUpgradingIt(t *testing.T) {
	got, err := Parse([]byte("lockVersion: 1\nroot:\n  name: app\n  version: 1.0.0\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.LockVersion != MinLockVersion {
		t.Errorf("LockVersion = %d, want %d preserved as written", got.LockVersion, MinLockVersion)
	}
}
