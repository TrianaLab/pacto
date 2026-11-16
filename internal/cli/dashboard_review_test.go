package cli

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// reviewRecorder is a fake cluster that allows every review and records which
// kind each was, with its namespace.
type reviewRecorder struct {
	mu    sync.Mutex
	asked []string
}

func (r *reviewRecorder) client() func(*rest.Config) (dynamic.Interface, error) {
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "resourceAttributes", "namespace")
		r.mu.Lock()
		r.asked = append(r.asked, a.GetResource().Resource+"/"+ns)
		r.mu.Unlock()
		o.Object["status"] = map[string]any{"allowed": true}
		return true, o, nil
	})
	return func(*rest.Config) (dynamic.Interface, error) { return c, nil }
}

// reviewFleet holds a service in payments and one known only from a registry.
func reviewFleet(t *testing.T) *fleet.Query {
	t.Helper()
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "reg", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}, FS: fstest.MapFS{}}, Digest: "sha256:reg"}},
		Targets: []fleet.RawTarget{{Scope: "payments", Kind: fleet.SourceKindKubernetes, Name: "web", Service: "web"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestClusterReviewLocalUsesLocal(t *testing.T) {
	r := &reviewRecorder{}
	ok, err := clusterReview(&rest.Config{}, r.client(), reviewLocal)(context.Background(), authn.Anonymous, "payments")
	if err != nil || !ok || !slices.Equal(r.asked, []string{"localsubjectaccessreviews/payments"}) {
		t.Errorf("local review: %v %v, asked %v", ok, err, r.asked)
	}
}

// dashboardReviewIdentity is a token-login dashboard over the recorder's cluster.
func dashboardReviewIdentity(t *testing.T, v *viper.Viper, r *reviewRecorder) *dashboardIdentity {
	t.Helper()
	v.Set("dashboard.token-login", true)
	d := deliveryDeps("alice", nil)
	d.dynamicFor = r.client()
	id, err := newDashboardIdentity(v, "0.0.0.0", &rest.Config{Host: "https://k"}, d)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestSingleNamespaceKeepsClusterReview: --namespace payments without
// --review keeps the cluster review, so a cluster reader still sees a
// service known only from a registry.
func TestSingleNamespaceKeepsClusterReview(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.namespace", "payments")
	r := &reviewRecorder{}
	id := dashboardReviewIdentity(t, v, r)
	q := reviewFleet(t)
	got, err := id.viewer.Query(context.Background(), authn.Person{User: "root"}, q)
	if err != nil || got != q {
		t.Fatalf("a cluster reader sees %v, %v", got, err)
	}
	if !slices.Equal(r.asked, []string{"subjectaccessreviews/"}) {
		t.Errorf("asked %v", r.asked)
	}
}

// TestLocalReviewModeOnlyWhenSet: only --review=local asks namespaced
// reviews, and then nobody sees the registry-only service.
func TestLocalReviewModeOnlyWhenSet(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.namespace", "payments,ledger")
	v.Set("dashboard.review", reviewLocal)
	r := &reviewRecorder{}
	id := dashboardReviewIdentity(t, v, r)
	got, err := id.viewer.Query(context.Background(), authn.Person{User: "root"}, reviewFleet(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got.SnapshotForSerialization().Services {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"web"}) || !slices.Equal(r.asked, []string{"localsubjectaccessreviews/payments"}) {
		t.Errorf("root sees %v, asked %v", names, r.asked)
	}
}

func TestLocalReviewNeedsNamespaces(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.review", reviewLocal)
	if _, err := newDashboardIdentity(v, "127.0.0.1", nil, defaultDeps()); !errors.Is(err, errLocalReviewNeedsNamespace) {
		t.Errorf("local without namespaces: %v", err)
	}
	v.Set("dashboard.review", "everywhere")
	if _, err := newDashboardIdentity(v, "127.0.0.1", nil, defaultDeps()); err == nil {
		t.Error("an unknown review mode starts")
	}
}

func TestMCPViewerLocalUsesLocal(t *testing.T) {
	d := defaultDeps()
	r := &reviewRecorder{}
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{}, nil }
	d.dynamicFor = r.client()
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	_ = cmd.Flags().Set("review", reviewLocal)
	if _, err := mcpFleetView(cmd, d); !errors.Is(err, errLocalReviewNeedsNamespace) {
		t.Fatalf("local without namespaces: %v", err)
	}
	_ = cmd.Flags().Set("namespace", "payments")
	_ = cmd.Flags().Set("transport", "http")
	_ = cmd.Flags().Set("auth-issuer", "https://idp.example")
	view, err := mcpFleetView(cmd, d)
	if err != nil {
		t.Fatal(err)
	}
	alice := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: authn.Person{User: "alice"}})
	if _, err := view(alice, nil, reviewFleet(t)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.asked, []string{"localsubjectaccessreviews/payments"}) {
		t.Errorf("asked %v", r.asked)
	}
}
