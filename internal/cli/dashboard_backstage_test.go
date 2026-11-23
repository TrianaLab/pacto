package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestBackstageBearerReaderSeesItsRBACScope: a Backstage backend sending a
// dashboard-audience ServiceAccount token reads only the namespaces RBAC
// lets that ServiceAccount list Pactos in.
func TestBackstageBearerReaderSeesItsRBACScope(t *testing.T) {
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Unauthorized") // an audience-only token is no bearer
	})
	c.PrependReactor("create", "tokenreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		o.Object["status"] = map[string]any{"authenticated": true, "audiences": []any{authn.DashboardAudience},
			"user": map[string]any{"username": "system:serviceaccount:pacto:backstage"}}
		return true, o, nil
	})
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "resourceAttributes", "namespace")
		user, _, _ := unstructured.NestedString(o.Object, "spec", "user")
		o.Object["status"] = map[string]any{"allowed": ns == "shop" && user == "system:serviceaccount:pacto:backstage"}
		return true, o, nil
	})
	v := viper.New()
	v.Set("dashboard.token-login", true)
	d := deliveryDeps("alice", nil)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return c, nil }
	id, err := newDashboardIdentity(v, "0.0.0.0", &rest.Config{Host: "https://k"}, homeEntries(&rest.Config{Host: "https://k"}), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "shop-svc", Service: "shop-svc"},
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "secret-svc", Service: "secret-svc"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	o := dashboard.Options{UI: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}
	id.options(&o)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = dashboard.NewServer(o).ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/api/backstage/entities", nil)
	req.Header.Set("Authorization", "Bearer sa-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	got := string(body)
	expect(t, resp.StatusCode == http.StatusOK && strings.Contains(got, "shop-svc") && !strings.Contains(got, "secret-svc"),
		"the bearer reader reads its RBAC scope: %d\n%s", resp.StatusCode, got)
}
