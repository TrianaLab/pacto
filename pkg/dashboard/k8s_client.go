package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sClient abstracts Kubernetes API operations for the dashboard.
type K8sClient interface {
	// Probe checks if the Kubernetes cluster is reachable.
	Probe(ctx context.Context) error
	// DiscoverCRD discovers the Pacto CRD group, version, and resource name.
	DiscoverCRD(ctx context.Context) (*CRDDiscovery, error)
	// ListJSON returns the raw JSON of all Pacto CRD resources.
	ListJSON(ctx context.Context, resource, namespace string) ([]byte, error)
	// GetJSON returns the raw JSON of a single Pacto CRD resource by name.
	GetJSON(ctx context.Context, resource, namespace, name string) ([]byte, error)
	// CountResources returns the number of Pacto CRD resources.
	CountResources(ctx context.Context, resource, namespace string) (int, error)
}

// CRDDiscovery holds the result of discovering the Pacto CRD on the cluster.
type CRDDiscovery struct {
	Found        bool
	Group        string
	Versions     []string
	Version      string // preferred or first version
	ResourceName string
}

// Package-level function variables for testing.
var (
	newK8sClientFunc            = newK8sGoClient
	inClusterConfigFunc         = rest.InClusterConfig
	newDiscoveryClientForConfig = func(c *rest.Config) (discovery.DiscoveryInterface, error) {
		return discovery.NewDiscoveryClientForConfig(c)
	}
	newDynamicForConfig = func(c *rest.Config) (dynamic.Interface, error) {
		return dynamic.NewForConfig(c)
	}
)

// k8sGoClient implements K8sClient using k8s.io/client-go.
type k8sGoClient struct {
	discovery discovery.DiscoveryInterface
	dynamic   dynamic.Interface
	group     string
	version   string // set after DiscoverCRD
}

func newK8sGoClient() (K8sClient, error) {
	config, err := buildK8sConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	config.Timeout = 5 * time.Second

	disc, err := newDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client: %w", err)
	}

	dyn, err := newDynamicForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &k8sGoClient{
		discovery: disc,
		dynamic:   dyn,
		group:     "pacto.trianalab.io",
	}, nil
}

func buildK8sConfig() (*rest.Config, error) {
	// Try in-cluster config first.
	config, err := inClusterConfigFunc()
	if err == nil {
		return config, nil
	}
	// Fall back to kubeconfig.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
}

func (c *k8sGoClient) Probe(ctx context.Context) error {
	_, err := c.discovery.ServerVersion()
	return err
}

func (c *k8sGoClient) DiscoverCRD(ctx context.Context) (*CRDDiscovery, error) {
	result := &CRDDiscovery{Group: c.group}

	groups, _, err := c.discovery.ServerGroupsAndResources()
	if err != nil {
		return result, fmt.Errorf("listing API groups: %w", err)
	}

	for _, g := range groups {
		if g.Name != c.group {
			continue
		}
		result.Found = true
		for _, v := range g.Versions {
			result.Versions = append(result.Versions, v.Version)
		}
		if g.PreferredVersion.Version != "" {
			result.Version = g.PreferredVersion.Version
		} else if len(result.Versions) > 0 {
			result.Version = result.Versions[0]
		}
		break
	}

	if !result.Found {
		return result, nil
	}

	// Discover the resource name for the CRD.
	gv := c.group + "/" + result.Version
	resources, err := c.discovery.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return result, fmt.Errorf("listing resources for %s: %w", gv, err)
	}

	for _, r := range resources.APIResources {
		if strings.EqualFold(r.Kind, "Pacto") {
			result.ResourceName = r.Name
			break
		}
	}
	if result.ResourceName == "" {
		result.ResourceName = "pactos" // fallback
	}

	c.version = result.Version
	return result, nil
}

func (c *k8sGoClient) gvr(resource string) schema.GroupVersionResource {
	version := c.version
	if version == "" {
		version = "v1alpha1"
	}
	return schema.GroupVersionResource{
		Group:    c.group,
		Version:  version,
		Resource: resource,
	}
}

func (c *k8sGoClient) ListJSON(ctx context.Context, resource, namespace string) ([]byte, error) {
	gvr := c.gvr(resource)
	var list any
	var err error
	if namespace != "" {
		list, err = c.dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		list, err = c.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

func (c *k8sGoClient) GetJSON(ctx context.Context, resource, namespace, name string) ([]byte, error) {
	gvr := c.gvr(resource)
	obj, err := c.dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

func (c *k8sGoClient) CountResources(ctx context.Context, resource, namespace string) (int, error) {
	gvr := c.gvr(resource)
	var err error
	if namespace != "" {
		list, e := c.dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		err = e
		if err == nil {
			return len(list.Items), nil
		}
	} else {
		list, e := c.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		err = e
		if err == nil {
			return len(list.Items), nil
		}
	}
	return 0, err
}
