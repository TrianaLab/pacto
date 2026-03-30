package dashboard

import (
	"context"
	"fmt"
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
// buildK8sConfig
// ---------------------------------------------------------------------------

func TestBuildK8sConfig_InCluster(t *testing.T) {
	orig := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://in-cluster:443"}, nil
	}
	t.Cleanup(func() { inClusterConfigFunc = orig })

	config, err := buildK8sConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Host != "https://in-cluster:443" {
		t.Errorf("expected in-cluster host, got %q", config.Host)
	}
}

func TestBuildK8sConfig_Kubeconfig(t *testing.T) {
	orig := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = orig })

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

	config, err := buildK8sConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Host != "https://test-cluster:6443" {
		t.Errorf("expected test-cluster host, got %q", config.Host)
	}
}

func TestBuildK8sConfig_NoConfig(t *testing.T) {
	orig := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = orig })

	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("HOME", dir)

	_, err := buildK8sConfig()
	if err == nil {
		t.Fatal("expected error when no kubeconfig exists")
	}
}

// ---------------------------------------------------------------------------
// newK8sGoClient
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

	origIC := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = origIC })

	client, err := newK8sGoClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewK8sGoClient_ConfigError(t *testing.T) {
	origIC := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = origIC })

	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("HOME", dir)

	_, err := newK8sGoClient()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewK8sGoClient_DiscoveryError(t *testing.T) {
	origIC := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = origIC })

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

	origDisc := newDiscoveryClientForConfig
	newDiscoveryClientForConfig = func(*rest.Config) (discovery.DiscoveryInterface, error) {
		return nil, fmt.Errorf("discovery creation failed")
	}
	t.Cleanup(func() { newDiscoveryClientForConfig = origDisc })

	_, err := newK8sGoClient()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewK8sGoClient_DynamicError(t *testing.T) {
	origIC := inClusterConfigFunc
	inClusterConfigFunc = func() (*rest.Config, error) {
		return nil, fmt.Errorf("not in cluster")
	}
	t.Cleanup(func() { inClusterConfigFunc = origIC })

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

	origDyn := newDynamicForConfig
	newDynamicForConfig = func(*rest.Config) (dynamic.Interface, error) {
		return nil, fmt.Errorf("dynamic creation failed")
	}
	t.Cleanup(func() { newDynamicForConfig = origDyn })

	_, err := newK8sGoClient()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// k8sGoClient.Probe
// ---------------------------------------------------------------------------

func newFakeDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{
		Fake:               &k8stesting.Fake{},
		FakedServerVersion: &version.Info{Major: "1", Minor: "28"},
	}
}

func TestK8sGoClient_Probe_Success(t *testing.T) {
	client := &k8sGoClient{discovery: newFakeDiscovery()}
	if err := client.Probe(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8sGoClient_Probe_Error(t *testing.T) {
	disc := newFakeDiscovery()
	disc.FakedServerVersion = nil
	disc.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("connection refused")
	})
	client := &k8sGoClient{discovery: disc}
	if err := client.Probe(context.Background()); err == nil {
		t.Error("expected error from Probe")
	}
}

// ---------------------------------------------------------------------------
// k8sGoClient.DiscoverCRD
// ---------------------------------------------------------------------------

func TestK8sGoClient_DiscoverCRD_Found(t *testing.T) {
	disc := newFakeDiscovery()
	disc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "pacto.trianalab.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "pactos", Kind: "Pacto"},
			},
		},
	}

	client := &k8sGoClient{discovery: disc, group: "pacto.trianalab.io"}
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
	if result.Version != "v1alpha1" {
		t.Errorf("expected version 'v1alpha1', got %q", result.Version)
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

	client := &k8sGoClient{discovery: disc, group: "pacto.trianalab.io"}
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

	client := &k8sGoClient{discovery: disc, group: "pacto.trianalab.io"}
	_, err := client.DiscoverCRD(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestK8sGoClient_DiscoverCRD_NoMatchingKind(t *testing.T) {
	disc := newFakeDiscovery()
	disc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "pacto.trianalab.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "pactorevisions", Kind: "PactoRevision"},
			},
		},
	}

	client := &k8sGoClient{discovery: disc, group: "pacto.trianalab.io"}
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

	client := &k8sGoClient{discovery: mock, group: "pacto.trianalab.io"}
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
					{GroupVersion: "pacto.trianalab.io/v1alpha1", Version: "v1alpha1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1alpha1"},
			},
		},
		resourceErr: fmt.Errorf("resource list failed"),
	}

	client := &k8sGoClient{discovery: mock, group: "pacto.trianalab.io"}
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
// k8sGoClient.gvr
// ---------------------------------------------------------------------------

func TestK8sGoClient_GVR_WithVersion(t *testing.T) {
	client := &k8sGoClient{group: "pacto.trianalab.io", version: "v1alpha1"}
	gvr := client.gvr("pactos")
	if gvr.Group != "pacto.trianalab.io" || gvr.Version != "v1alpha1" || gvr.Resource != "pactos" {
		t.Errorf("unexpected GVR: %v", gvr)
	}
}

func TestK8sGoClient_GVR_DefaultVersion(t *testing.T) {
	client := &k8sGoClient{group: "pacto.trianalab.io"}
	gvr := client.gvr("pactos")
	if gvr.Version != "v1alpha1" {
		t.Errorf("expected default version 'v1alpha1', got %q", gvr.Version)
	}
}

// ---------------------------------------------------------------------------
// k8sGoClient.ListJSON / GetJSON / CountResources
// ---------------------------------------------------------------------------

func newFakeDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "pactos"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gvr: "PactoList"},
		objects...)
}

func newTestPactoObject(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "pacto.trianalab.io/v1alpha1",
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

func TestK8sGoClient_ListJSON_WithNamespace(t *testing.T) {
	obj := newTestPactoObject("svc-a", "default")
	dyn := newFakeDynamicClient(obj)
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	data, err := client.ListJSON(context.Background(), "pactos", "default")
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
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	data, err := client.ListJSON(context.Background(), "pactos", "")
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
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	_, err := client.ListJSON(context.Background(), "pactos", "default")
	if err == nil {
		t.Error("expected error")
	}
}

func TestK8sGoClient_GetJSON_Success(t *testing.T) {
	obj := newTestPactoObject("my-svc", "default")
	dyn := newFakeDynamicClient(obj)
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	data, err := client.GetJSON(context.Background(), "pactos", "default", "my-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

func TestK8sGoClient_GetJSON_Error(t *testing.T) {
	dyn := newFakeDynamicClient()
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	_, err := client.GetJSON(context.Background(), "pactos", "default", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent resource")
	}
}

func TestK8sGoClient_CountResources_WithNamespace(t *testing.T) {
	obj1 := newTestPactoObject("svc-a", "default")
	obj2 := newTestPactoObject("svc-b", "default")
	dyn := newFakeDynamicClient(obj1, obj2)
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	count, err := client.CountResources(context.Background(), "pactos", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}
}

func TestK8sGoClient_CountResources_AllNamespaces(t *testing.T) {
	obj := newTestPactoObject("svc-a", "default")
	dyn := newFakeDynamicClient(obj)
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	count, err := client.CountResources(context.Background(), "pactos", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestK8sGoClient_CountResources_Error_WithNamespace(t *testing.T) {
	dyn := newFakeDynamicClient()
	dyn.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("list failed")
	})
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	_, err := client.CountResources(context.Background(), "pactos", "default")
	if err == nil {
		t.Error("expected error")
	}
}

func TestK8sGoClient_CountResources_Error_AllNamespaces(t *testing.T) {
	dyn := newFakeDynamicClient()
	dyn.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("list failed")
	})
	client := &k8sGoClient{dynamic: dyn, group: "pacto.trianalab.io", version: "v1alpha1"}

	_, err := client.CountResources(context.Background(), "pactos", "")
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

	got := readCurrentKubeContext()
	if got != "my-cluster" {
		t.Errorf("readCurrentKubeContext() = %q, want %q", got, "my-cluster")
	}
}

func TestReadCurrentKubeContext_NoConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "nonexistent"))

	got := readCurrentKubeContext()
	if got != "" {
		t.Errorf("readCurrentKubeContext() = %q, want empty", got)
	}
}

func TestReadCurrentKubeContext_MalformedConfig(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte("not: valid: yaml: ["), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfigPath)

	got := readCurrentKubeContext()
	if got != "" {
		t.Errorf("readCurrentKubeContext() = %q, want empty for malformed config", got)
	}
}
