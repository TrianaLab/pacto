package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
)

// homeEntries is the one clusterless context the fleet reads, on cfg's
// server: a single-cluster install whose id is not read.
func homeEntries(cfg *rest.Config) func(context.Context) []app.K8sEntry {
	return func(context.Context) []app.K8sEntry { return []app.K8sEntry{{Config: cfg}} }
}

// oneReview is the reviews of a single cluster whose items carry no id.
func oneReview(r authn.Review) authn.Reviews {
	return func(context.Context) map[string]authn.Review { return map[string]authn.Review{"": r} }
}

// ambientEntries reads d's ambient cluster as the one clusterless context
// the fleet reads, as a single-cluster install whose id is not read.
func ambientEntries(d *deps) func(*app.Service, context.Context, bool, []string) []app.K8sEntry {
	return func(*app.Service, context.Context, bool, []string) []app.K8sEntry {
		cfg, err := d.clusterConfig()
		if err != nil {
			return nil
		}
		return []app.K8sEntry{{Config: cfg}}
	}
}

// hostClusters is one fake API server per host: allow answers each review,
// uids is each server's kube-system UID (none: the read is forbidden), and
// asked records every review as host/namespace.
type hostClusters struct {
	mu    sync.Mutex
	asked []string
	allow func(host, ns string) bool
	uids  map[string]string
}

func (h *hostClusters) dynamicFor(cfg *rest.Config) (dynamic.Interface, error) {
	host := cfg.Host
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "resourceAttributes", "namespace")
		h.mu.Lock()
		h.asked = append(h.asked, host+"/"+ns)
		h.mu.Unlock()
		o.Object["status"] = map[string]any{"allowed": h.allow(host, ns)}
		return true, o, nil
	})
	c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		uid := h.uids[host]
		if uid == "" {
			return true, nil, errors.New("namespaces is forbidden")
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace",
			"metadata": map[string]any{"name": "kube-system", "uid": uid}}}, nil
	})
	return c, nil
}

func (h *hostClusters) askedOf(host string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, a := range h.asked {
		if len(a) > len(host) && a[:len(host)+1] == host+"/" {
			out = append(out, a[len(host)+1:])
		}
	}
	return out
}

func entry(name, host, uid string) app.K8sEntry {
	return app.K8sEntry{Name: name, Config: &rest.Config{Host: host}, UID: uid, Host: host}
}

func entriesOf(es ...app.K8sEntry) func(context.Context) []app.K8sEntry {
	return func(context.Context) []app.K8sEntry { return es }
}

// reviewDeps is defaultDeps reviewing through h.
func reviewDeps(h *hostClusters) deps {
	d := defaultDeps()
	d.dynamicFor = h.dynamicFor
	return d
}

func everyNS(string, string) bool { return true }

func TestClusterReviewsKeysByUID(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	set := clusterReviews(entriesOf(entry("eu-prod", "https://eu", "uid-eu"), entry("us-prod", "https://us", "uid-us")), homeCluster{}, reviewDeps(h), reviewCluster)
	m := set.Reviews(context.Background())
	if len(m) != 2 || m["uid-eu"] == nil || m["uid-us"] == nil {
		t.Fatalf("reviewers %v", m)
	}
	if ok, err := m["uid-us"](context.Background(), authn.Person{User: "alice"}, "prod", authn.PactoResource); !ok || err != nil ||
		!slices.Equal(h.askedOf("https://us"), []string{"prod"}) || len(h.askedOf("https://eu")) != 0 {
		t.Errorf("us reviewed through %v, eu %v: %v", h.askedOf("https://us"), h.askedOf("https://eu"), err)
	}
}

func TestClusterReviewsDedupesOneCluster(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	alias := entry("eu-admin", "https://eu", "uid-eu")
	alias.Alias = "eu-prod"
	m := clusterReviews(entriesOf(entry("eu-prod", "https://eu", "uid-eu"), alias), homeCluster{}, reviewDeps(h), reviewCluster).Reviews(context.Background())
	if len(m) != 1 || m["uid-eu"] == nil {
		t.Errorf("reviewers %v", m)
	}
}

func TestClusterReviewsConflictUIDHasNoReviewer(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	clone := entry("restored", "https://restored", "uid-eu")
	clone.Conflict = "eu-prod"
	m := clusterReviews(entriesOf(entry("eu-prod", "https://eu", "uid-eu"), clone, entry("us-prod", "https://us", "uid-us")),
		homeCluster{}, reviewDeps(h), reviewCluster).Reviews(context.Background())
	if len(m) != 1 || m["uid-us"] == nil {
		t.Errorf("reviewers %v: a UID two servers report has none", m)
	}
}

func TestClusterReviewsRetriesIdentityAfterFailureTTL(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	calls := 0
	us := entry("us-prod", "https://us", "")
	broken := app.K8sEntry{Name: "ap-prod", Err: errors.New("no such context")}
	entries := func(context.Context) []app.K8sEntry {
		calls++
		if calls == 1 {
			return []app.K8sEntry{entry("eu-prod", "https://eu", "uid-eu"), us, broken}
		}
		return []app.K8sEntry{entry("eu-prod", "https://eu", "uid-eu"), entry("us-prod", "https://us", "uid-us")}
	}
	now := time.Unix(1000, 0)
	set := clusterReviews(entries, homeCluster{}, reviewDeps(h), reviewCluster)
	set.now = func() time.Time { return now }
	if m := set.Reviews(context.Background()); len(m) != 1 || m["uid-eu"] == nil {
		t.Fatalf("an unidentified and a broken context get no reviewer: %v", m)
	}
	now = now.Add(failureTTL / 2)
	if set.Reviews(context.Background()); calls != 1 {
		t.Errorf("resolved again within failureTTL: %d", calls)
	}
	now = now.Add(failureTTL)
	if m := set.Reviews(context.Background()); calls != 2 || len(m) != 2 {
		t.Errorf("after failureTTL: %d calls, %v", calls, m)
	}
	now = now.Add(time.Hour)
	if set.Reviews(context.Background()); calls != 2 {
		t.Errorf("identified entries are resolved again: %d", calls)
	}
}

func TestClusterReviewsSingleUnidentifiedIsBlankKey(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	us := entry("us-prod", "https://us", "")
	home := homeCluster{Config: &rest.Config{Host: "https://us"}, UID: func(context.Context) (string, bool) { return "", false }}
	set := clusterReviews(entriesOf(us), home, reviewDeps(h), reviewCluster)
	m := set.Reviews(context.Background())
	if len(m) != 1 || m[""] == nil {
		t.Fatalf("reviewers %v", m)
	}
	if key, ok := set.Home(context.Background()); key != "" || !ok {
		t.Errorf("the clusterless entry on the sign-in server is home: %q %v", key, ok)
	}
	// On another server it is no home: the sign-in cluster's id decides.
	home = homeCluster{Config: &rest.Config{Host: "https://home"}, UID: func(context.Context) (string, bool) { return "uid-home", true }}
	set = clusterReviews(entriesOf(us), home, reviewDeps(h), reviewCluster)
	if key, ok := set.Home(context.Background()); key != "uid-home" || !ok {
		t.Errorf("home %q %v", key, ok)
	}
	// One of several unidentified contexts is never reviewed.
	if m := clusterReviews(entriesOf(us, entry("eu-prod", "https://eu", "uid-eu")), home, reviewDeps(h), reviewCluster).Reviews(context.Background()); m[""] != nil {
		t.Errorf("reviewers %v", m)
	}
}

func TestNewHomeClusterReadsItsUIDOnce(t *testing.T) {
	h := &hostClusters{allow: everyNS, uids: map[string]string{"https://home": "uid-home"}}
	d := reviewDeps(h)
	if uid, ok := newHomeCluster(nil, d).UID(context.Background()); ok || uid != "" {
		t.Errorf("no config: %q", uid)
	}
	home := newHomeCluster(&rest.Config{Host: "https://home"}, d)
	if uid, ok := home.UID(context.Background()); !ok || uid != "uid-home" {
		t.Errorf("home uid %q %v", uid, ok)
	}
	h.uids = nil
	if uid, ok := home.UID(context.Background()); !ok || uid != "uid-home" {
		t.Errorf("a read uid is kept: %q %v", uid, ok)
	}
	unread := newHomeCluster(&rest.Config{Host: "https://home"}, d)
	if _, ok := unread.UID(context.Background()); ok {
		t.Error("a forbidden read names a uid")
	}
	h.uids = map[string]string{"https://home": "uid-home"}
	if _, ok := unread.UID(context.Background()); ok {
		t.Error("a failed read is asked again within failureTTL")
	}
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("no client") }
	if _, ok := newHomeCluster(&rest.Config{Host: "https://home"}, d).UID(context.Background()); ok {
		t.Error("no client names a uid")
	}
}

// TestClusterlessContextNeverReviewedByHome: an HTTP MCP server reading only
// --context us-prod, whose id it cannot read, reviews us-prod's items with
// us-prod's own credentials and never with the identified ambient (sign-in)
// cluster's.
func TestClusterlessContextNeverReviewedByHome(t *testing.T) {
	h := &hostClusters{allow: func(host, ns string) bool { return host == "https://home" }, uids: map[string]string{"https://home": "uid-home"}}
	d := reviewDeps(h)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://home"}, nil }
	var gotContexts []string
	d.resolveK8s = func(_ *app.Service, _ context.Context, includeCurrent bool, contexts []string) []app.K8sEntry {
		gotContexts = contexts
		if includeCurrent {
			t.Error("--k8s was not passed")
		}
		return []app.K8sEntry{entry("us-prod", "https://us", "")}
	}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	for k, v := range map[string]string{"transport": "http", "auth-issuer": "https://idp.example", "context": "us-prod"} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	view, _ := mcpFleetView(cmd, app.NewService(nil, nil), d)
	alice := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: authn.Person{User: "alice"}})
	got, err := view(alice, nil, reviewFleet(t))
	if err != nil || len(got.SnapshotForSerialization().Services) != 0 {
		t.Fatalf("us-prod refuses alice: %v", err)
	}
	if len(h.askedOf("https://home")) != 0 || len(h.askedOf("https://us")) == 0 || !slices.Equal(gotContexts, []string{"us-prod"}) {
		t.Errorf("home asked %v, us asked %v, contexts %v", h.askedOf("https://home"), h.askedOf("https://us"), gotContexts)
	}
}

// TestClusterReviewsNoSourceReviewsAtHome: with no Kubernetes source (an HTTP
// MCP server reading only a registry), the items without a cluster id are
// reviewed in the sign-in cluster, and with no sign-in cluster nobody is.
func TestClusterReviewsNoSourceReviewsAtHome(t *testing.T) {
	h := &hostClusters{allow: everyNS}
	home := homeCluster{Config: &rest.Config{Host: "https://home"}, UID: func(context.Context) (string, bool) { return "", false }}
	set := clusterReviews(entriesOf(), home, reviewDeps(h), reviewCluster)
	m := set.Reviews(context.Background())
	if len(m) != 1 || m[""] == nil {
		t.Fatalf("reviewers %v; want the sign-in cluster for clusterless items", m)
	}
	if ok, err := m[""](context.Background(), authn.Person{User: "alice"}, "", authn.PactoResource); !ok || err != nil || !slices.Equal(h.askedOf("https://home"), []string{""}) {
		t.Errorf("asked %v: %v %v", h.asked, ok, err)
	}
	if key, ok := set.Home(context.Background()); key != "" || !ok {
		t.Errorf("home %q %v", key, ok)
	}
	if m := clusterReviews(entriesOf(), homeCluster{}, reviewDeps(h), reviewCluster).Reviews(context.Background()); len(m) != 0 {
		t.Errorf("no sign-in cluster: reviewers %v", m)
	}
}

// TestSameServerComparesWhatTheServerIs: one CA given as a file and inline,
// and one host with a trailing slash, are one server.
func TestSameServerComparesWhatTheServerIs(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := &rest.Config{Host: "https://prod", TLSClientConfig: rest.TLSClientConfig{CAFile: ca}}
	inline := &rest.Config{Host: "https://prod/", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("PEM")}}
	if !sameServer(file, inline) || sameServer(file, nil) || sameServer(file, &rest.Config{Host: "https://other"}) {
		t.Error("sameServer must compare the host and the CA's bytes")
	}
}
