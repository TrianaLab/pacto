package catalog

import (
	"errors"
	"testing"
)

func TestNewContentIDAcceptsOnlyAClosedSchemeAndARealDigest(t *testing.T) {
	good := ociID("ok").Digest
	cases := []struct {
		name   string
		scheme ContentScheme
		digest string
		ok     bool
	}{
		{"oci digest", SchemeOCI, good, true},
		{"local digest", SchemeLocal, good, true},
		{"empty scheme", "", good, false},
		{"invented scheme", "tag", good, false},
		{"a tag is not identity", SchemeOCI, "latest", false},
		{"a version is not identity", SchemeOCI, "1.0.0", false},
		{"a service name is not identity", SchemeOCI, "payments", false},
		{"empty digest", SchemeOCI, "", false},
		{"truncated hex", SchemeOCI, "sha256:abcd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := NewContentID(tc.scheme, tc.digest)
			if tc.ok {
				if err != nil {
					t.Fatalf("NewContentID: %v", err)
				}
				if id.Zero() || id.Scheme != tc.scheme || id.Digest != tc.digest {
					t.Errorf("id = %+v, want the validated pair", id)
				}
				return
			}
			if err == nil {
				t.Fatalf("NewContentID(%q, %q) succeeded", tc.scheme, tc.digest)
			}
			if !errors.Is(err, ErrInvalidContentID) {
				t.Errorf("err = %v, want ErrInvalidContentID", err)
			}
			if !id.Zero() {
				t.Errorf("a rejected pair returned %+v", id)
			}
		})
	}
}

func TestContentIDStringIsDisplayOnly(t *testing.T) {
	id := ociID("display")
	if got, want := id.String(), "oci:"+id.Digest; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	// One digest under two schemes is two identities, and they say so.
	if ociID("same").String() == localID("same").String() {
		t.Error("the scheme vanished from the rendering")
	}
}

// Every comparator is exercised field by field: a comparator that ignores its
// later fields still sorts, just not deterministically, and only a per-field
// assertion catches that.

func TestCompareContentIDOrdersSchemeThenDigest(t *testing.T) {
	a, b := ociID("cmp-a"), ociID("cmp-b")
	if compareContentID(ContentID{SchemeLocal, a.Digest}, ContentID{SchemeOCI, a.Digest}) >= 0 {
		t.Error("the scheme is not compared first")
	}
	if compareContentID(a, a) != 0 {
		t.Error("equal identities do not compare equal")
	}
	if compareContentID(a, b) == compareContentID(b, a) {
		t.Error("the digest is not compared")
	}
}

func TestCompareServiceIDOrdersDomainThenName(t *testing.T) {
	if compareServiceID(ServiceID{Domain: "a"}, ServiceID{Domain: "b"}) >= 0 {
		t.Error("the domain is not compared first")
	}
	if compareServiceID(ServiceID{Name: "a"}, ServiceID{Name: "b"}) >= 0 {
		t.Error("the name is not compared")
	}
	if compareServiceID(ServiceID{"d", "n"}, ServiceID{"d", "n"}) != 0 {
		t.Error("equal services do not compare equal")
	}
}

func TestCompareDeclarationOrdersContentThenIndex(t *testing.T) {
	a, b := ociID("cmp-a"), ociID("cmp-b")
	if compareDeclaration(DeclarationID{a, 9}, DeclarationID{b, 0}) >= 0 {
		t.Error("the declaring content is not compared first")
	}
	if compareDeclaration(DeclarationID{a, 0}, DeclarationID{a, 1}) >= 0 {
		t.Error("the declaration index is not compared")
	}
}

func TestComparePathOrdersRootThenSteps(t *testing.T) {
	a, b := ociID("cmp-a"), ociID("cmp-b")
	if comparePath(Path{Root: 0}, Path{Root: 1}) >= 0 {
		t.Error("the root is not compared first")
	}
	short := Path{Root: 0, Steps: []DeclarationID{{From: a}}}
	long := Path{Root: 0, Steps: []DeclarationID{{From: a}, {From: b}}}
	if comparePath(short, long) >= 0 {
		t.Error("a prefix does not sort before its extension")
	}
	if comparePath(short, clonePath(short)) != 0 {
		t.Error("a clone does not compare equal to its original")
	}
}

func TestCompareEdgeOrdersDeclarationThenTarget(t *testing.T) {
	a, b := ociID("cmp-a"), ociID("cmp-b")
	if compareEdge(Edge{Declaration: DeclarationID{a, 0}, To: b}, Edge{Declaration: DeclarationID{a, 1}}) >= 0 {
		t.Error("the declaration is not compared first")
	}
	if compareEdge(Edge{To: a}, Edge{To: b}) >= 0 {
		t.Error("the target is not compared")
	}
}

func TestCompareUnresolvedOrdersDeclarationThenRefThenName(t *testing.T) {
	a := ociID("cmp-a")
	if compareUnresolved(Unresolved{Declaration: DeclarationID{a, 0}, Ref: "z"},
		Unresolved{Declaration: DeclarationID{a, 1}}) >= 0 {
		t.Error("the declaration is not compared first")
	}
	if compareUnresolved(Unresolved{Ref: "a", Name: "z"}, Unresolved{Ref: "b"}) >= 0 {
		t.Error("the reference is not compared")
	}
	if compareUnresolved(Unresolved{Name: "a"}, Unresolved{Name: "b"}) >= 0 {
		t.Error("the declared name is not compared")
	}
}

func TestCompareLimitationOrdersCodeThenRef(t *testing.T) {
	if compareLimitation(Limitation{Code: "A", Ref: "z"}, Limitation{Code: "B"}) >= 0 {
		t.Error("the code is not compared first")
	}
	if compareLimitation(Limitation{Ref: "a"}, Limitation{Ref: "b"}) >= 0 {
		t.Error("the reference is not compared")
	}
}

func TestCompareConflictOrdersEveryDistinguishingField(t *testing.T) {
	a := ociID("cmp-a")
	if compareConflict(Conflict{Kind: ConflictContent}, Conflict{Kind: ConflictVersion}) >= 0 {
		t.Error("the kind is not compared first")
	}
	if compareConflict(Conflict{Service: ServiceID{Name: "a"}}, Conflict{Service: ServiceID{Name: "b"}}) >= 0 {
		t.Error("the service is not compared")
	}
	if compareConflict(Conflict{Version: "1"}, Conflict{Version: "2"}) >= 0 {
		t.Error("the version is not compared")
	}
	if compareConflict(Conflict{Declaration: DeclarationID{a, 0}}, Conflict{Declaration: DeclarationID{a, 1}}) >= 0 {
		t.Error("the declaration is not compared")
	}
}

func TestRankIsDerivedFromTheShortestPath(t *testing.T) {
	for depth, want := range map[int]Rank{0: RankRoot, 1: RankDirect, 2: RankTransitive, 7: RankTransitive} {
		if got := rankForDepth(depth); got != want {
			t.Errorf("rankForDepth(%d) = %q, want %q", depth, got, want)
		}
	}
}

func TestBoundsTakeDefaultsAndCeilings(t *testing.T) {
	if got := (Bounds{}).effective(); got != (Bounds{
		MaxRoots: DefaultMaxRoots, MaxRevisions: DefaultMaxRevisions, MaxEdges: DefaultMaxEdges,
		MaxDepth: DefaultMaxDepth, MaxPaths: DefaultMaxPaths, MaxPathLength: DefaultMaxPathLength,
		MaxUnresolved: DefaultMaxUnresolved, MaxConflicts: DefaultMaxConflicts,
		MaxLimitations: DefaultMaxLimitations,
	}) {
		t.Errorf("zero bounds = %+v, want every default", got)
	}
	huge := Bounds{
		MaxRoots: 1 << 20, MaxRevisions: 1 << 20, MaxEdges: 1 << 20, MaxDepth: 1 << 20,
		MaxPaths: 1 << 20, MaxPathLength: 1 << 20, MaxUnresolved: 1 << 20,
		MaxConflicts: 1 << 20, MaxLimitations: 1 << 20,
	}
	if got := huge.effective(); got != (Bounds{
		MaxRoots: CeilingMaxRoots, MaxRevisions: CeilingMaxRevisions, MaxEdges: CeilingMaxEdges,
		MaxDepth: CeilingMaxDepth, MaxPaths: CeilingMaxPaths, MaxPathLength: CeilingMaxPathLength,
		MaxUnresolved: CeilingMaxUnresolved, MaxConflicts: CeilingMaxConflicts,
		MaxLimitations: CeilingMaxLimitations,
	}) {
		t.Errorf("unbounded bounds = %+v, want every ceiling", got)
	}
	if got := clampBound(-1, 7, 9); got != 7 {
		t.Errorf("clampBound(-1) = %d, want the default", got)
	}
	if got := clampBound(3, 7, 9); got != 3 {
		t.Errorf("clampBound(3) = %d, want the caller's value", got)
	}
}

func TestResolveErrorCarriesItsSanitizedMessage(t *testing.T) {
	err := &ResolveError{Code: ReasonAuthFailed, Message: "the registry refused the credentials"}
	if err.Error() != "the registry refused the credentials" {
		t.Errorf("Error() = %q", err.Error())
	}
	if r := reasonFrom(err); r.Code != ReasonAuthFailed || r.Message != err.Message {
		t.Errorf("reasonFrom = %+v, want the resolver's own classification", r)
	}
	// A *ResolveError without a code is as untrustworthy as any other error.
	if r := reasonFrom(&ResolveError{Message: "leaky"}); r.Code != ReasonUnavailable || r.Message == "leaky" {
		t.Errorf("reasonFrom = %+v, want the generic reason", r)
	}
}

func TestEncodeCannotBeForgedByFieldContents(t *testing.T) {
	if encode("a", "bc") == encode("ab", "c") {
		t.Error("length framing is missing: two field splits produced one encoding")
	}
	if encode("a/b", "c") == encode("a", "b/c") {
		t.Error("a delimiter inside a field split it")
	}
	if encode("", "a") == encode("a", "") {
		t.Error("empty fields are not positional")
	}
}
