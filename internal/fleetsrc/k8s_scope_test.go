package fleetsrc

import (
	"context"
	"errors"
	"slices"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// nsFakeClient answers each namespace's list from lists (missing: no items)
// and errs, and records the namespaces asked.
type nsFakeClient struct {
	found bool
	lists map[string]string
	errs  map[string]error
	asked []string
}

func (f *nsFakeClient) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return &k8sclient.CRDDiscovery{Found: f.found, ResourceName: "pactos"}, nil
}

// ListPage reads as a cluster with no workloads.
func (f *nsFakeClient) ListPage(context.Context, schema.GroupVersionResource, string, string) ([]byte, string, error) {
	return []byte(`{"items":[]}`), "", nil
}

func (f *nsFakeClient) ListJSON(_ context.Context, _ k8sclient.CRDDiscovery, ns string) ([]byte, error) {
	f.asked = append(f.asked, ns)
	if err := f.errs[ns]; err != nil {
		return nil, err
	}
	if l, ok := f.lists[ns]; ok {
		return []byte(l), nil
	}
	return []byte(`{"items":[]}`), nil
}

func (f *nsFakeClient) ClusterIdentity(context.Context, string) (kubecluster.Identity, error) {
	return kubecluster.Identity{}, errors.New("unused")
}

func pactoIn(ns, name string) string {
	return `{"items":[{"metadata":{"name":"` + name + `","namespace":"` + ns + `"},"status":{"contract":{"serviceName":"` + name + `","resolvedRef":"ghcr.io/x/` + name + `:1.0.0"}}}]}`
}

func collectScoped(t *testing.T, c *nsFakeClient, namespaces ...string) *fleet.Collection {
	t.Helper()
	col, err := NewK8sSource("c1", nil, c, namespaces).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return col
}

func limitationsOf(col *fleet.Collection, code string) []string {
	var out []string
	for _, l := range col.Limitations {
		if l.Code == code {
			out = append(out, l.Message)
		}
	}
	return out
}

func TestK8sSourceListsEachNamespace(t *testing.T) {
	c := &nsFakeClient{found: true}
	collectScoped(t, c, "payments", "ledger", "", "ledger")
	if !slices.Equal(c.asked, []string{"ledger", "payments"}) {
		t.Errorf("asked %q, want [ledger payments]", c.asked)
	}
	c = &nsFakeClient{found: true}
	collectScoped(t, c)
	if !slices.Equal(c.asked, []string{""}) {
		t.Errorf("unscoped asked %q, want one all-namespaces list", c.asked)
	}
}

func TestK8sSourceScopedIsPartial(t *testing.T) {
	col := collectScoped(t, &nsFakeClient{found: true}, "payments", "ledger")
	if col.State == nil || col.State.Status != fleet.SourcePartial || !slices.Equal(col.State.Namespaces, []string{"ledger", "payments"}) || col.State.NamespacesTotal != 2 {
		t.Fatalf("state = %+v, want partial over [ledger payments]", col.State)
	}
	want := "source c1 watches 2 namespaces (ledger and payments); services in any other namespace are unknown"
	if got := limitationsOf(col, fleet.LimitationK8sNamespaceScoped); !slices.Equal(got, []string{want}) {
		t.Errorf("scope limitations = %q, want %q", got, want)
	}
}

func TestK8sSourceScopedOneNamespaceSingular(t *testing.T) {
	col := collectScoped(t, &nsFakeClient{found: true}, "payments")
	want := "source c1 watches 1 namespace (payments); services in any other namespace are unknown"
	if got := limitationsOf(col, fleet.LimitationK8sNamespaceScoped); !slices.Equal(got, []string{want}) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestK8sSourceScopedTruncatesNames(t *testing.T) {
	col := collectScoped(t, &nsFakeClient{found: true}, "a", "b", "c", "d", "e", "f")
	want := "source c1 watches 6 namespaces (a, b, c and 3 more); services in any other namespace are unknown"
	if got := limitationsOf(col, fleet.LimitationK8sNamespaceScoped); !slices.Equal(got, []string{want}) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func buildOne(t *testing.T, src fleet.Source) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return snap
}

func TestK8sSourceScopedEmptyIsPartialNotEmpty(t *testing.T) {
	snap := buildOne(t, NewK8sSource("c1", nil, &nsFakeClient{found: true}, []string{"payments"}))
	if snap.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness = %s, want partial", snap.Completeness)
	}
}

func TestK8sSourceScopedNoCRDIsPartial(t *testing.T) {
	src := NewK8sSource("c1", nil, &nsFakeClient{}, []string{"payments", "ledger"})
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if col.State == nil || col.State.Status != fleet.SourcePartial || len(limitationsOf(col, fleet.LimitationK8sNamespaceScoped)) != 1 {
		t.Errorf("no-CRD collection = %+v, want the partial scope", col)
	}
	if snap := buildOne(t, src); snap.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness = %s, want partial", snap.Completeness)
	}
}

func TestK8sSourceOneNamespaceForbiddenIsPartialAndNamed(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "pacto.trianalab.io", Resource: "pactos"}, "", errors.New("no"))
	col := collectScoped(t, &nsFakeClient{found: true, lists: map[string]string{"payments": pactoIn("payments", "pay")}, errs: map[string]error{"ledger": forbidden}}, "ledger", "payments")
	if len(col.Targets) != 1 || col.Targets[0].Scope != "payments" {
		t.Errorf("targets = %+v, want the payments one", col.Targets)
	}
	if !slices.Equal(col.State.Namespaces, []string{"payments"}) {
		t.Errorf("namespaces = %q, want [payments]", col.State.Namespaces)
	}
	want := "source c1 could not read namespace ledger: forbidden"
	if got := limitationsOf(col, fleet.LimitationK8sNamespaceUnreadable); !slices.Equal(got, []string{want}) {
		t.Errorf("unreadable = %q, want %q", got, want)
	}
	if got := limitationsOf(col, fleet.LimitationK8sNamespaceScoped); len(got) != 1 || got[0] != "source c1 watches 1 namespace (payments); services in any other namespace are unknown" {
		t.Errorf("scope = %q", got)
	}
}

func TestK8sSourceUnreadableReasonClasses(t *testing.T) {
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "typo")
	col := collectScoped(t, &nsFakeClient{found: true, errs: map[string]error{"typo": notFound, "flaky": errors.New("boom")}}, "typo", "flaky", "ok")
	got := limitationsOf(col, fleet.LimitationK8sNamespaceUnreadable)
	want := []string{"source c1 could not read namespace flaky: error", "source c1 could not read namespace typo: not found"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestK8sSourceAllNamespacesFailIsUnavailable(t *testing.T) {
	c := &nsFakeClient{found: true, errs: map[string]error{"a": errors.New("x"), "b": errors.New("y")}}
	if _, err := NewK8sSource("c1", nil, c, []string{"a", "b"}).Collect(context.Background()); err == nil {
		t.Fatal("want an error when every namespace fails")
	}
	snap := buildOne(t, NewK8sSource("c1", nil, c, []string{"a", "b"}))
	if snap.Sources[0].Status != fleet.SourceUnavailable {
		t.Errorf("status = %s, want unavailable", snap.Sources[0].Status)
	}
}

func TestK8sSourceUnscopedUnchanged(t *testing.T) {
	col := collectScoped(t, &nsFakeClient{found: true, lists: map[string]string{"": pactoIn("payments", "pay")}})
	if col.State != nil || len(col.Limitations) != 0 {
		t.Errorf("unscoped collection carries state %+v and limitations %+v", col.State, col.Limitations)
	}
}

func TestK8sSourceContractRefsPerNamespace(t *testing.T) {
	c := &nsFakeClient{found: true, lists: map[string]string{"a": pactoIn("a", "one"), "b": pactoIn("b", "two")}}
	refs, err := ContractRefs(context.Background(), c, []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ghcr.io/x/one", "ghcr.io/x/one:1.0.0", "ghcr.io/x/two", "ghcr.io/x/two:1.0.0"}
	if !slices.Equal(refs, want) || !slices.Equal(c.asked, []string{"a", "b"}) {
		t.Errorf("refs = %q asked %q, want %q over [a b]", refs, c.asked, want)
	}
}
