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
	if r, ok := got.Reference("policy", "sec"); !ok || r.Digest != "sha256:s" {
		t.Errorf("Reference(policy,sec) lookup failed")
	}
	if _, ok := got.Reference("config", "missing"); ok {
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
