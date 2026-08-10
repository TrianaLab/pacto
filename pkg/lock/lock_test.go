package lock

import (
	"fmt"
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

// The occurrence identity keeps its parts apart, so no legal name can forge
// another declaration's identity. The joined closure path of lockVersion 2 could
// be forged exactly this way: a scope named "a/policy:b" rendered to the same
// text as a policy "b" declared inside the bundle a scope named "a" reached.
func TestOccurrenceIsInjectiveOverLegalNames(t *testing.T) {
	forger := Reference{Kind: "config", Name: "a/policy:b", Source: "oci", Digest: "sha256:x"}
	victim := Reference{From: "oci:sha256:c", Kind: "policy", Name: "b", Source: "oci", Digest: "sha256:y"}
	if forger.Occurrence() == victim.Occurrence() {
		t.Errorf("a legal scope name forged another declaration's identity: %v", forger.Occurrence())
	}
	// Kind is part of the identity, not a prefix on the name.
	cfgRef := Reference{Kind: "config", Name: "policy:x"}
	polRef := Reference{Kind: "policy", Name: "x"}
	if cfgRef.Occurrence() == polRef.Occurrence() {
		t.Error("a name may start with another kind's label without colliding with it")
	}
	// Two entries of the same declaration ARE one identity.
	if (Reference{From: "oci:sha256:c", Kind: "config", Name: "s"}).Occurrence() !=
		(Reference{From: "oci:sha256:c", Kind: "config", Name: "s", Digest: "sha256:z"}).Occurrence() {
		t.Error("the identity must not depend on what the reference resolved to")
	}
}

// DestinationID is the edge that turns the reference set into a graph: it is in
// the same namespace as From, so an entry declared by another entry's target is
// recognizable without storing any route.
func TestDestinationIDSharesTheFromNamespace(t *testing.T) {
	parent := Reference{Kind: "config", Name: "mid", Source: "oci", Digest: "sha256:mid"}
	child := Reference{From: parent.DestinationID(), Kind: "config", Name: "deep", Source: "local", ContentHash: "sha256:deep"}
	if parent.DestinationID() != "oci:sha256:mid" {
		t.Errorf("oci destination = %q", parent.DestinationID())
	}
	if child.DestinationID() != "local:sha256:deep" {
		t.Errorf("local destination = %q", child.DestinationID())
	}
	if child.From != parent.DestinationID() {
		t.Error("the child's declaring identity must be the parent's destination")
	}
	// "" is reserved for the root, so no entry can claim to be root-declared.
	if (Reference{Source: "oci"}).DestinationID() != "" {
		t.Error("an unpinned entry has no destination identity")
	}
	// An oci entry never answers with a local hash, and vice versa.
	if (Reference{Source: "oci", ContentHash: "sha256:x"}).DestinationID() != "" {
		t.Error("a content hash is not an oci destination")
	}
	if (Reference{Source: "local", Digest: "sha256:x"}).DestinationID() != "" {
		t.Error("a digest is not a local destination")
	}
}

// Occurrence.String is diagnostic text, not an identity, and says so by never
// being used as one. It must still name both the declaration and its declarer.
func TestOccurrenceStringNamesDeclarationAndDeclarer(t *testing.T) {
	root := Occurrence{Kind: "config", Name: "settings"}.String()
	if !strings.Contains(root, "root") || !strings.Contains(root, "settings") {
		t.Errorf("root occurrence renders as %q", root)
	}
	nested := Occurrence{From: "oci:sha256:abc", Kind: "policy", Name: "limits"}.String()
	if !strings.Contains(nested, "oci:sha256:abc") || !strings.Contains(nested, "limits") {
		t.Errorf("nested occurrence renders as %q", nested)
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
	legacy := &Lock{LockVersion: RootOccurrenceLockVersion - 1, References: []Reference{
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

// The published compatibility matrix, as a test rather than as prose in a doc
// that can drift from it.
//
//	v1  readable   no declaring contract at all      root lookup: no
//	v2  readable   root sound, transitive unsound    root lookup: yes
//	v3  readable   injective for every legal name    root lookup: yes
//	v4  refused    written by a newer pacto
//
// v2 keeps its root lookup deliberately: the delimiter flaw could forge a
// transitive path but never the empty string, so "the root declared this" was
// always decidable. Nothing may read a v2 entry's transitive attribution, which
// is why RootOccurrenceLockVersion is the only version gate in the package.
func TestLockVersionCompatibilityMatrix(t *testing.T) {
	body := "\npacto:\n  version: 1.4.0\nroot:\n  name: app\n  version: 1.0.0\nreferences:\n" +
		"  - kind: config\n    name: settings\n    source: oci\n    ref: oci://r/c\n    digest: sha256:c\n"
	for _, tc := range []struct {
		version      int
		readable     bool
		rootResolves bool
	}{
		{1, true, false},
		{2, true, true},
		{3, true, true},
		{CurrentLockVersion + 1, false, false},
	} {
		l, err := Parse([]byte(fmt.Sprintf("lockVersion: %d%s", tc.version, body)))
		if !tc.readable {
			if err == nil {
				t.Errorf("lockVersion %d must be refused, not reinterpreted", tc.version)
			}
			continue
		}
		if err != nil {
			t.Errorf("lockVersion %d must stay readable: %v", tc.version, err)
			continue
		}
		if l.LockVersion != tc.version {
			t.Errorf("lockVersion %d was rewritten to %d on read", tc.version, l.LockVersion)
		}
		if _, ok := l.RootReference("config", "settings"); ok != tc.rootResolves {
			t.Errorf("lockVersion %d root lookup = %v, want %v", tc.version, ok, tc.rootResolves)
		}
	}
}

// Marshal always writes the current schema, so a lock read at an older version
// and written back declares what it now actually contains.
func TestMarshalWritesTheCurrentSchemaVersion(t *testing.T) {
	l := &Lock{LockVersion: 1, Root: RootInfo{Name: "app", Version: "1.0.0"}}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("lockVersion: %d", CurrentLockVersion)) {
		t.Errorf("marshal must declare the current schema:\n%s", data)
	}
	if l.LockVersion != 1 {
		t.Error("Marshal must not mutate its receiver")
	}
}
