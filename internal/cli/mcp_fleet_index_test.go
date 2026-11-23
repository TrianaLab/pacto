package cli

import (
	"context"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
)

// indexFleetMode is a pacto mcp --fleet server over transport reading the
// fixture's whole index and nothing else, with flags added.
func indexFleetMode(t *testing.T, f *indexFixture, d deps, transport string, flags map[string]string) *pactomcp.FleetMode {
	t.Helper()
	cmd := newMCPCommand(f.svc, "v", d)
	cmd.SetContext(t.Context())
	set := map[string]string{"transport": transport, "fleet": "true", "local": "", "index": f.index()}
	for k, v := range flags {
		set[k] = v
	}
	for k, v := range set {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	m, err := buildFleetMode(cmd, f.svc, d)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestMCPFleetReadsWholeIndex: a stdio fleet server given --index reads
// every listed service, under the registry-index source the fleet tools
// report.
func TestMCPFleetReadsWholeIndex(t *testing.T) {
	f := newIndexFixture(t)
	q, err := indexFleetMode(t, f, defaultDeps(), "stdio", nil).Src(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snap := q.SnapshotForSerialization()
	var ids []string
	for _, s := range snap.Sources {
		ids = append(ids, s.ID)
	}
	if !slices.Contains(ids, "registry-index") || len(snap.Services) < 2 {
		t.Fatalf("sources %v, %d services", ids, len(snap.Services))
	}
}

// TestMCPHTTPFleetIndexFollowsRestrict: over HTTP the index revisions are
// served under the caller's view: a caller who may read one namespace sees
// no registry-only service, a cluster-wide caller sees them.
func TestMCPHTTPFleetIndexFollowsRestrict(t *testing.T) {
	f := newIndexFixture(t)
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "resourceAttributes", "namespace")
		user, _, _ := unstructured.NestedString(o.Object, "spec", "user")
		o.Object["status"] = map[string]any{"allowed": user == "root" || ns == "shop"}
		return true, o, nil
	})
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{}, nil }
	d.resolveK8s = ambientEntries(&d)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return c, nil }
	m := indexFleetMode(t, f, d, "http", map[string]string{"auth-issuer": "https://idp.example"})
	q, err := m.Src(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	names := func(user string) []string {
		ctx := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: authn.Person{User: user}})
		v, err := m.Opts.View(ctx, nil, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range v.SnapshotForSerialization().Services {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names("alice"); slices.Contains(got, "web") {
		t.Errorf("a one-namespace caller sees %v", got)
	}
	if got := names("root"); !slices.Contains(got, "web") || !slices.Contains(got, "payments") {
		t.Errorf("a cluster-wide caller sees %v", got)
	}
}
