package k8sclient

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	fakediscovery "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

// ---------------------------------------------------------------------------
// builders.config
// ---------------------------------------------------------------------------

func TestBuildK8sConfigSuppressesAPIWarnings(t *testing.T) {
	t.Run("in-cluster", func(t *testing.T) {
		b := defaultBuilders()
		b.inCluster = func() (*rest.Config, error) { return &rest.Config{Host: "https://example.test"}, nil }

		cfg, _, err := b.config("")
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		if _, ok := cfg.WarningHandlerWithContext.(rest.NoWarnings); !ok {
			t.Fatalf("want rest.NoWarnings, got %T", cfg.WarningHandlerWithContext)
		}
	})

	t.Run("kubeconfig-fallback", func(t *testing.T) {
		b := defaultBuilders()
		b.inCluster = func() (*rest.Config, error) { return nil, fmt.Errorf("not in cluster") }

		dir := t.TempDir()
		kubeconfig := filepath.Join(dir, "config")
		_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-cluster:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test-token
`), 0o644)
		t.Setenv("KUBECONFIG", kubeconfig)

		cfg, _, err := b.config("")
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		if _, ok := cfg.WarningHandlerWithContext.(rest.NoWarnings); !ok {
			t.Fatalf("want rest.NoWarnings, got %T", cfg.WarningHandlerWithContext)
		}
	})
}

func TestBuildK8sConfig_InCluster(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://in-cluster:443"}, nil
	}

	config, inCluster, err := b.config("")
	if err != nil || !inCluster {
		t.Fatalf("unexpected error: %v (in cluster %v)", err, inCluster)
	}
	if config.Host != "https://in-cluster:443" {
		t.Errorf("expected in-cluster host, got %q", config.Host)
	}
}

func TestBuildK8sConfig_Kubeconfig(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-cluster:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test-token
`), 0o644)
	t.Setenv("KUBECONFIG", kubeconfig)

	config, inCluster, err := b.config("")
	if err != nil || inCluster {
		t.Fatalf("unexpected error: %v (in cluster %v)", err, inCluster)
	}
	if config.Host != "https://test-cluster:6443" {
		t.Errorf("expected test-cluster host, got %q", config.Host)
	}
}

func TestBuildK8sConfig_NoConfig(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("HOME", dir)

	_, _, err := b.config("")
	if err == nil {
		t.Fatal("expected error when no kubeconfig exists")
	}
}

// ---------------------------------------------------------------------------
// NewGoClient
// ---------------------------------------------------------------------------

func TestNewK8sGoClient_Success(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0o644)
	t.Setenv("KUBECONFIG", kubeconfig)

	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	client, err := b.goClient("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil || client.(interface{ InCluster() bool }).InCluster() {
		t.Fatal("expected a non-nil client built outside the cluster")
	}
}

func TestNewK8sGoClient_ConfigError(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("HOME", dir)

	_, err := b.goClient("")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewK8sGoClient_DiscoveryError(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0o644)
	t.Setenv("KUBECONFIG", kubeconfig)

	b.discovery = func(*rest.Config) (discovery.DiscoveryInterface, error) {
		return nil, fmt.Errorf("discovery creation failed")
	}

	_, err := b.goClient("")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewK8sGoClient_DynamicError(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}

	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0o644)
	t.Setenv("KUBECONFIG", kubeconfig)

	b.dynamic = func(*rest.Config) (dynamic.Interface, error) {
		return nil, fmt.Errorf("dynamic creation failed")
	}

	_, err := b.goClient("")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// fake discovery
// ---------------------------------------------------------------------------

func newFakeDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{
		Fake:               &k8stesting.Fake{},
		FakedServerVersion: &version.Info{Major: "1", Minor: "28"},
	}
}

// ---------------------------------------------------------------------------
// k8sGoClient.DiscoverCRD
// ---------------------------------------------------------------------------

func TestK8sGoClient_DiscoverCRD_Found(t *testing.T) {
	disc := newFakeDiscovery()
	disc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "pacto.trianalab.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "pactos", Kind: "Pacto"},
			},
		},
	}

	client := &k8sGoClient{discovery: disc}
	result, err := client.DiscoverCRD(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Found {
		t.Error("expected Found=true")
	}
	if result.ResourceName != "pactos" {
		t.Errorf("expected resource name 'pactos', got %q", result.ResourceName)
	}
	if result.Version != "v1" {
		t.Errorf("expected version 'v1', got %q", result.Version)
	}
}

func TestK8sGoClient_DiscoverCRD_NotFound(t *testing.T) {
	disc := newFakeDiscovery()
	disc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "other.group.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "others", Kind: "Other"},
			},
		},
	}

	client := &k8sGoClient{discovery: disc}
	result, err := client.DiscoverCRD(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Found {
		t.Error("expected Found=false")
	}
}

func TestK8sGoClient_DiscoverCRD_Error(t *testing.T) {
	disc := newFakeDiscovery()
	disc.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("API error")
	})

	client := &k8sGoClient{discovery: disc}
	_, err := client.DiscoverCRD(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestK8sGoClient_DiscoverCRD_NoMatchingKind(t *testing.T) {
	disc := newFakeDiscovery()
	disc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "pacto.trianalab.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "pactorevisions", Kind: "PactoRevision"},
			},
		},
	}

	client := &k8sGoClient{discovery: disc}
	result, err := client.DiscoverCRD(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Found {
		t.Error("expected Found=true (group exists)")
	}
	// Fallback resource name.
	if result.ResourceName != "pactos" {
		t.Errorf("expected fallback resource name 'pactos', got %q", result.ResourceName)
	}
}

func TestK8sGoClient_DiscoverCRD_VersionFallback(t *testing.T) {
	// Use a mock that returns groups without PreferredVersion.
	mock := &mockDiscoveryForCRD{
		groups: []*metav1.APIGroup{
			{
				Name: "pacto.trianalab.io",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "pacto.trianalab.io/v1beta1", Version: "v1beta1"},
				},
				// PreferredVersion intentionally empty.
			},
		},
		resources: map[string]*metav1.APIResourceList{
			"pacto.trianalab.io/v1beta1": {
				GroupVersion: "pacto.trianalab.io/v1beta1",
				APIResources: []metav1.APIResource{
					{Name: "pactos", Kind: "Pacto"},
				},
			},
		},
	}

	client := &k8sGoClient{discovery: mock}
	result, err := client.DiscoverCRD(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version != "v1beta1" {
		t.Errorf("expected version 'v1beta1', got %q", result.Version)
	}
}

func TestK8sGoClient_DiscoverCRD_ResourceListError(t *testing.T) {
	mock := &mockDiscoveryForCRD{
		groups: []*metav1.APIGroup{
			{
				Name: "pacto.trianalab.io",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "pacto.trianalab.io/v1", Version: "v1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
		},
		resourceErr: fmt.Errorf("resource list failed"),
	}

	client := &k8sGoClient{discovery: mock}
	_, err := client.DiscoverCRD(context.Background())
	if err == nil {
		t.Error("expected error from ServerResourcesForGroupVersion")
	}
}

// mockDiscoveryForCRD embeds FakeDiscovery but overrides ServerGroupsAndResources
// and ServerResourcesForGroupVersion for fine-grained control.
type mockDiscoveryForCRD struct {
	fakediscovery.FakeDiscovery
	groups      []*metav1.APIGroup
	resources   map[string]*metav1.APIResourceList
	resourceErr error
}

func (m *mockDiscoveryForCRD) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	var resList []*metav1.APIResourceList
	for _, rl := range m.resources {
		resList = append(resList, rl)
	}
	return m.groups, resList, nil
}

func (m *mockDiscoveryForCRD) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	if m.resourceErr != nil {
		return nil, m.resourceErr
	}
	if rl, ok := m.resources[gv]; ok {
		return rl, nil
	}
	return nil, fmt.Errorf("group version %q not found", gv)
}

// ---------------------------------------------------------------------------
// k8sGoClient.ListJSON
// ---------------------------------------------------------------------------

func newFakeDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "pacto.trianalab.io", Version: "v1", Resource: "pactos"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gvr: "PactoList"},
		objects...)
}

func newTestPactoObject(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "pacto.trianalab.io/v1",
			"kind":       "Pacto",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"status": map[string]any{
				"contractStatus": "Compliant",
			},
		},
	}
}

// pactos is what DiscoverCRD finds for the operator's CRD.
var pactos = CRDDiscovery{Found: true, Version: "v1", ResourceName: "pactos"}

func TestK8sGoClient_ListJSON_WithNamespace(t *testing.T) {
	obj := newTestPactoObject("svc-a", "default")
	dyn := newFakeDynamicClient(obj)
	client := &k8sGoClient{dynamic: dyn}

	data, err := client.ListJSON(context.Background(), pactos, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

func TestK8sGoClient_ListJSON_AllNamespaces(t *testing.T) {
	obj := newTestPactoObject("svc-a", "default")
	dyn := newFakeDynamicClient(obj)
	client := &k8sGoClient{dynamic: dyn}

	data, err := client.ListJSON(context.Background(), pactos, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

func TestK8sGoClient_ListJSON_Error(t *testing.T) {
	dyn := newFakeDynamicClient()
	dyn.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("list failed")
	})
	client := &k8sGoClient{dynamic: dyn}

	_, err := client.ListJSON(context.Background(), pactos, "default")
	if err == nil {
		t.Error("expected error")
	}
}

func TestReadCurrentKubeContext_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte(`apiVersion: v1
kind: Config
current-context: my-cluster
contexts:
- context:
    cluster: my-cluster
  name: my-cluster
clusters:
- cluster:
    server: https://localhost:6443
  name: my-cluster
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfigPath)

	got := CurrentKubeContext()
	if got != "my-cluster" {
		t.Errorf("CurrentKubeContext() = %q, want %q", got, "my-cluster")
	}
}

// TestCurrentKubeContextInAPod: a pod reads its in-cluster config as the
// current one, so a mounted kubeconfig's current-context never names it and a
// --context naming that context is read as the other cluster it is.
func TestCurrentKubeContextInAPod(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte("apiVersion: v1\nkind: Config\ncurrent-context: us-prod\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfigPath)
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) { return &rest.Config{Host: "https://10.96.0.1:443"}, nil }
	if got := b.currentContext(); got != "" {
		t.Errorf("in a pod the current context is %q, want none", got)
	}
	b.inCluster = func() (*rest.Config, error) { return nil, rest.ErrNotInCluster }
	if got := b.currentContext(); got != "us-prod" {
		t.Errorf("outside a pod the current context is %q, want us-prod", got)
	}
}

func TestReadCurrentKubeContext_NoConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "nonexistent"))

	got := CurrentKubeContext()
	if got != "" {
		t.Errorf("CurrentKubeContext() = %q, want empty", got)
	}
}

func TestReadCurrentKubeContext_MalformedConfig(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte("not: valid: yaml: ["), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfigPath)

	got := CurrentKubeContext()
	if got != "" {
		t.Errorf("CurrentKubeContext() = %q, want empty for malformed config", got)
	}
}

func TestNewDynamic(t *testing.T) {
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) { return &rest.Config{Host: "https://in-cluster:443"}, nil }
	if c, err := b.newDynamic(); err != nil || c == nil {
		t.Fatalf("NewDynamic = %v, %v", c, err)
	}

	b.inCluster = func() (*rest.Config, error) { return nil, fmt.Errorf("not in cluster") }
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("HOME", t.TempDir())
	if _, err := b.newDynamic(); err == nil {
		t.Fatal("no config anywhere: want error")
	}
}

// TestExportedConstructorsUseTheAmbientKubeconfig drives the production
// builders: outside a pod in-cluster config fails and the kubeconfig answers.
func TestExportedConstructorsUseTheAmbientKubeconfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	kubeconfig := filepath.Join(t.TempDir(), "config")
	_ = os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0o644)
	t.Setenv("KUBECONFIG", kubeconfig)
	if c, err := NewGoClient(); err != nil || c == nil {
		t.Fatalf("NewGoClient = %v, %v", c, err)
	}
	if c, err := NewDynamic(); err != nil || c == nil {
		t.Fatalf("NewDynamic = %v, %v", c, err)
	}
	c, err := Config()
	if err != nil || c.Host != "https://test:6443" {
		t.Fatalf("Config = %v, %v", c, err)
	}
	if ok, err := Served(&rest.Config{Host: "http://127.0.0.1:1"}, "delivery.pacto.trianalab.io/v1"); ok || err == nil {
		t.Fatal("an unreachable cluster is an error, not a cluster without the API")
	}
}

func TestServed(t *testing.T) {
	b := defaultBuilders()
	fake := newFakeDiscovery()
	fake.Resources = []*metav1.APIResourceList{{GroupVersion: "delivery.pacto.trianalab.io/v1"}}
	b.discovery = func(*rest.Config) (discovery.DiscoveryInterface, error) { return fake, nil }
	if ok, err := b.served(&rest.Config{}, "delivery.pacto.trianalab.io/v1"); !ok || err != nil {
		t.Fatalf("served answers from discovery: %v", err)
	}
	if ok, err := b.served(&rest.Config{}, "other/v1"); ok || err != nil {
		t.Fatalf("a NotFound is not served, and no error: %v", err)
	}
	// Discovery failing for any other reason is an error, never "not served".
	fake.PrependReactor("get", "resource", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("connection refused")
	})
	if ok, err := b.served(&rest.Config{}, "delivery.pacto.trianalab.io/v1"); ok || err == nil {
		t.Fatal("a failed discovery call is an error")
	}
	b.discovery = func(*rest.Config) (discovery.DiscoveryInterface, error) { return nil, fmt.Errorf("no") }
	if ok, err := b.served(&rest.Config{}, "x/v1"); ok || err == nil {
		t.Fatal("no discovery client is an error")
	}
}

// groupsDiscovery serves a fixed group list, or err.
type groupsDiscovery struct {
	fakediscovery.FakeDiscovery
	groups []metav1.APIGroup
	err    error
}

func (d *groupsDiscovery) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{Groups: d.groups}, d.err
}

func TestPreferredVersions(t *testing.T) {
	v := func(vs ...string) []metav1.GroupVersionForDiscovery {
		var out []metav1.GroupVersionForDiscovery
		for _, x := range vs {
			out = append(out, metav1.GroupVersionForDiscovery{Version: x})
		}
		return out
	}
	disc := &groupsDiscovery{groups: []metav1.APIGroup{
		{Name: "helm.toolkit.fluxcd.io", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v2"}, Versions: v("v2beta2", "v2")},
		{Name: "argoproj.io", Versions: v("v1alpha1")},
		{Name: "apps", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"}},
		{Name: "empty.example.com"},
	}}
	got, err := (&k8sGoClient{discovery: disc}).PreferredVersions(context.Background(), "helm.toolkit.fluxcd.io", "argoproj.io", "source.toolkit.fluxcd.io", "empty.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"helm.toolkit.fluxcd.io": "v2", "argoproj.io": "v1alpha1"}; !maps.Equal(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
	disc.err = errors.New("unreachable")
	if _, err := (&k8sGoClient{discovery: disc}).PreferredVersions(context.Background(), "argoproj.io"); err == nil {
		t.Error("a discovery error read as no group served")
	}
}

// twoContextKubeconfig writes a kubeconfig whose current context is "eu-prod"
// and which also names "us-prod", and points KUBECONFIG at it.
func twoContextKubeconfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
current-context: eu-prod
clusters:
- cluster:
    server: https://eu.example:6443
  name: eu
- cluster:
    server: https://us.example:6443
  name: us
contexts:
- context:
    cluster: eu
    user: u
  name: eu-prod
- context:
    cluster: us
    user: u
  name: us-prod
users:
- name: u
  user:
    token: t
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

// TestNamedContextSkipsInClusterConfig: a named context loads that context
// from the kubeconfig even where in-cluster config would answer, so a pod can
// read another cluster.
func TestNamedContextSkipsInClusterConfig(t *testing.T) {
	twoContextKubeconfig(t)
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) {
		t.Fatal("in-cluster config read for a named context")
		return nil, nil
	}
	cfg, _, err := b.config("us-prod")
	if err != nil || cfg.Host != "https://us.example:6443" {
		t.Fatalf("config = %v, %v", cfg, err)
	}
	if _, ok := cfg.WarningHandlerWithContext.(rest.NoWarnings); !ok {
		t.Fatalf("warnings handler %T", cfg.WarningHandlerWithContext)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if cfg, err := ConfigFor("us-prod"); err != nil || cfg.Host != "https://us.example:6443" {
		t.Fatalf("ConfigFor = %v, %v", cfg, err)
	}
	if c, err := NewGoClientFor("us-prod"); err != nil || c == nil {
		t.Fatalf("NewGoClientFor = %v, %v", c, err)
	}
}

func TestUnknownContextIsAnError(t *testing.T) {
	twoContextKubeconfig(t)
	if _, err := ConfigFor("ap-prod"); err == nil {
		t.Fatal("an unknown context loaded")
	}
	if _, err := NewGoClientFor("ap-prod"); err == nil {
		t.Fatal("an unknown context built a client")
	}
}

// TestServerOf: the CA compares as its bytes whether inline or a file, an
// unreadable file as its path, and the host without a trailing slash.
func TestServerOf(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("PEM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cfg      *rest.Config
		host, ca string
	}{
		{nil, "", ""},
		{&rest.Config{Host: "https://a/", TLSClientConfig: rest.TLSClientConfig{CAFile: ca}}, "https://a", "PEM"},
		{&rest.Config{Host: "https://a", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("PEM"), CAFile: "/nope"}}, "https://a", "PEM"},
		{&rest.Config{Host: "https://a", TLSClientConfig: rest.TLSClientConfig{CAFile: "/nope"}}, "https://a", "file:/nope"},
	} {
		if h, c := ServerOf(tc.cfg); h != tc.host || c != tc.ca {
			t.Errorf("ServerOf(%+v) = %q, %q; want %q, %q", tc.cfg, h, c, tc.host, tc.ca)
		}
	}
}
