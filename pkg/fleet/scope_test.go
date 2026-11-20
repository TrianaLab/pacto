package fleet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// scopedFleet is one Kubernetes source that watches payments and ledger, read
// both, has a target in payments only, and could not read audit; and an
// unscoped registry source.
func scopedFleet(t *testing.T) *Query {
	t.Helper()
	k := SourceKindKubernetes
	read := []string{"ledger", "payments"}
	src := NewMemorySource("c1", k, &Collection{
		Targets: []RawTarget{{Scope: "payments", Kind: k, Name: "pay", Service: "pay", Compliance: StatusCompliant}},
		State:   &SourceState{Status: SourcePartial, Namespaces: read, NamespacesTotal: len(read)},
		Limitations: []Limitation{
			NamespaceScopeLimitation("c1", read, 0),
			NamespaceUnreadableLimitation("c1", "audit", "forbidden"),
		},
	})
	// A second, unscoped source: its state carries no namespaces to restrict.
	plain := NewMemorySource("registry", "oci", &Collection{})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src, plain)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func scopeMessages(ls []Limitation, code string) []string {
	var out []string
	for _, l := range ls {
		if l.Code == code {
			out = append(out, l.Message)
		}
	}
	return out
}

func TestSourceStateKeepsNamespaces(t *testing.T) {
	s := scopedFleet(t).SnapshotForSerialization()
	if len(s.Sources) != 2 || !slices.Equal(s.Sources[0].Namespaces, []string{"ledger", "payments"}) || s.Sources[0].NamespacesTotal != 2 {
		t.Fatalf("sources = %+v, want the declared namespaces kept", s.Sources)
	}
	if s.Completeness != CompletenessPartial {
		t.Errorf("completeness = %s, want partial", s.Completeness)
	}
}

func TestScopedNamespacesAreReviewed(t *testing.T) {
	if got := namespaceNames(scopedFleet(t).Namespaces()); !slices.Equal(got, []string{"ledger", "payments"}) {
		t.Errorf("Namespaces = %q, want the read ones only: a review where the source could not read is likely refused too", got)
	}
}

func TestMetaSourceNamespacesCapped(t *testing.T) {
	ns := make([]string, 100)
	for i := range ns {
		ns[i] = "ns" + strconv.Itoa(i)
	}
	q := NewQuery(&FleetSnapshot{Sources: []SourceState{{ID: "c1", Namespaces: ns}}})
	for _, srcs := range [][]SourceState{q.ProductMeta().Sources, q.meta().Sources} {
		if len(srcs[0].Namespaces) != MaxSourceNamespaces || srcs[0].NamespacesTotal != 100 {
			t.Errorf("meta source carries %d namespaces, total %d; want %d of 100", len(srcs[0].Namespaces), srcs[0].NamespacesTotal, MaxSourceNamespaces)
		}
	}
	if len(q.snap.Sources[0].Namespaces) != 100 {
		t.Error("the cap reached back into the snapshot")
	}
}

func TestRestrictScrubsWatchedNamespaces(t *testing.T) {
	s := scopedFleet(t).Restrict(anyCluster(func(ns string) bool { return ns == "payments" }), anyClusterKind(nil)).SnapshotForSerialization()
	b, err := json.Marshal(struct {
		Sources     []SourceState
		Limitations []Limitation
	}{s.Sources, s.Limitations})
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"ledger", "audit"} {
		if strings.Contains(string(b), hidden) {
			t.Errorf("a restricted answer names %s: %s", hidden, b)
		}
	}
	if !slices.Equal(s.Sources[0].Namespaces, []string{"payments"}) || s.Sources[0].NamespacesTotal != 2 {
		t.Errorf("source = %+v, want [payments] of 2", s.Sources[0])
	}
}

func TestRestrictKeepsScopedLimitation(t *testing.T) {
	s := scopedFleet(t).Restrict(anyCluster(func(ns string) bool { return ns == "payments" }), anyClusterKind(nil)).SnapshotForSerialization()
	want := "source c1 watches 1 namespace you may read (payments) and 1 more; services in any other namespace are unknown"
	if got := scopeMessages(s.Limitations, LimitationK8sNamespaceScoped); !slices.Equal(got, []string{want}) {
		t.Errorf("scope = %q, want %q", got, want)
	}

	none := scopedFleet(t).Restrict(anyCluster(func(string) bool { return false }), anyClusterKind(nil)).SnapshotForSerialization()
	want = "source c1 watches 2 namespaces you may not read; services in any other namespace are unknown"
	if got := scopeMessages(none.Limitations, LimitationK8sNamespaceScoped); !slices.Equal(got, []string{want}) {
		t.Errorf("scope = %q, want %q", got, want)
	}
	if len(none.Sources[0].Namespaces) != 0 || none.Sources[0].NamespacesTotal != 2 {
		t.Errorf("source = %+v, want no names of 2", none.Sources[0])
	}
}

func TestRestrictKeepsReadableUnreadableNamespace(t *testing.T) {
	s := scopedFleet(t).Restrict(anyCluster(func(ns string) bool { return ns != "ledger" && ns != "" }), anyClusterKind(nil)).SnapshotForSerialization()
	want := []string{"source c1 could not read namespace audit: forbidden"}
	if got := scopeMessages(s.Limitations, LimitationK8sNamespaceUnreadable); !slices.Equal(got, want) {
		t.Errorf("unreadable = %q, want %q", got, want)
	}
}

func TestRestrictHidesWatchedNamespaceWithoutTargets(t *testing.T) {
	q := scopedFleet(t)
	if q.Restrict(anyCluster(func(ns string) bool { return ns == "payments" || ns == "audit" }), anyClusterKind(nil)) == q {
		t.Error("hiding an empty watched namespace must restrict the answer")
	}
}

func TestJoinNames(t *testing.T) {
	for in, want := range map[string]string{
		"":          "",
		"a":         "a",
		"a,b":       "a and b",
		"a,b,c,d":   "a, b, c and d",
		"a,b,c,d,e": "a, b, c and 2 more",
	} {
		var names []string
		if in != "" {
			names = strings.Split(in, ",")
		}
		if got := joinNames(names); got != want {
			t.Errorf("joinNames(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnreadableNamespaceParse(t *testing.T) {
	if _, ok := unreadableNamespace(Limitation{Code: LimitationK8sNamespaceScoped}); ok {
		t.Error("another code parsed")
	}
	if _, ok := unreadableNamespace(Limitation{Code: LimitationK8sNamespaceUnreadable, Message: "garbled"}); ok {
		t.Error("a garbled message parsed")
	}
	if ns, ok := unreadableNamespace(NamespaceUnreadableLimitation("s", "ledger", "forbidden")); !ok || ns != "ledger" {
		t.Errorf("parsed %q %v", ns, ok)
	}
}

func TestRestrictRebuildsScopeWhenEveryWatchedNamespaceIsVisible(t *testing.T) {
	k := SourceKindKubernetes
	read := []string{"payments"}
	scoped := NewMemorySource("c1", k, &Collection{
		State:       &SourceState{Status: SourcePartial, Namespaces: read, NamespacesTotal: 1},
		Limitations: []Limitation{NamespaceScopeLimitation("c1", read, 0)},
	})
	other := NewMemorySource("c2", k, &Collection{Targets: []RawTarget{{Scope: "secret", Kind: k, Name: "qqq", Service: "qqq", Compliance: StatusCompliant}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, scoped, other)
	if err != nil {
		t.Fatal(err)
	}
	s := NewQuery(snap).Restrict(anyCluster(func(ns string) bool { return ns == "payments" }), anyClusterKind(nil)).SnapshotForSerialization()
	want := []string{"source c1 watches 1 namespace (payments); services in any other namespace are unknown"}
	if got := scopeMessages(s.Limitations, LimitationK8sNamespaceScoped); !slices.Equal(got, want) {
		t.Errorf("scope = %q, want %q", got, want)
	}
}

// TestRestrictHiddenAllReadsAsScoped: a reader who may see none of the watched
// namespaces gets no names and the total, and the field doc says that shape is
// a scoped source, never every namespace.
func TestRestrictHiddenAllReadsAsScoped(t *testing.T) {
	s := scopedFleet(t).Restrict(anyCluster(func(string) bool { return false }), anyClusterKind(nil)).SnapshotForSerialization()
	b, err := json.Marshal(s.Sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"namespaces"`) || !strings.Contains(string(b), `"namespacesTotal":2`) {
		t.Errorf("source = %s, want no names and namespacesTotal 2", b)
	}
	f, _ := reflect.TypeFor[SourceState]().FieldByName("Namespaces")
	if doc := f.Tag.Get("doc"); !strings.Contains(doc, "empty with namespacesTotal above 0 means every namespace it read is hidden from you") {
		t.Errorf("Namespaces doc = %q, want the hidden-all shape documented", doc)
	}
}

// TestUnreadableNamespaceHiddenFromRestrictedReader: a namespace the source
// could not read is never reviewed, so a restricted reader never sees it named.
func TestUnreadableNamespaceHiddenFromRestrictedReader(t *testing.T) {
	q := scopedFleet(t)
	visible := map[string]bool{}
	for _, ns := range namespaceNames(q.Namespaces()) {
		visible[ns] = true
	}
	s := q.Restrict(anyCluster(func(ns string) bool { return visible[ns] }), anyClusterKind(nil)).SnapshotForSerialization()
	if got := scopeMessages(s.Limitations, LimitationK8sNamespaceUnreadable); len(got) != 0 {
		t.Errorf("unreadable = %q, want none for a reader whose namespaces were reviewed", got)
	}
}

func TestHiddenByScope(t *testing.T) {
	visible := func(ns string) bool { return ns == "a" }
	for _, tc := range []struct {
		l    Limitation
		want bool
	}{
		{NamespaceScopeLimitation("k8s", []string{"a"}, 0), true},
		{NamespaceUnreadableLimitation("k8s", "b", "forbidden"), true},
		{NamespaceUnreadableLimitation("k8s", "a", "forbidden"), false},
		{Limitation{Code: "X", Message: "b"}, false},
	} {
		if got := hiddenByScope(tc.l, anyCluster(visible), nil); got != tc.want {
			t.Errorf("hiddenByScope(%+v) = %v, want %v", tc.l, got, tc.want)
		}
	}
}
