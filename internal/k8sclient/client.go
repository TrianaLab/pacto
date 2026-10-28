// Package k8sclient is the shared, dashboard-independent Kubernetes access seam
// for reading Pacto CRD status from a cluster. It is consumed by both the
// dashboard and the fleet's live Kubernetes source, so neither owns a private
// client. It never appears in the platform-neutral core (see the architecture
// boundary test): it is an integration-side adapter that speaks client-go.
package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// PactoGroup is the API group of the operator's Pacto CRD.
const PactoGroup = "pacto.trianalab.io"

// PactosGVR is the operator's Pacto resource at the version it serves, for
// callers that read it directly rather than through DiscoverCRD.
var PactosGVR = schema.GroupVersionResource{Group: PactoGroup, Version: "v1alpha1", Resource: "pactos"}

// K8sClient abstracts the Kubernetes API operations Pacto needs to read Pacto
// CRD status.
type K8sClient interface {
	// DiscoverCRD discovers the Pacto CRD group, version, and resource name.
	DiscoverCRD(ctx context.Context) (*CRDDiscovery, error)
	// ListJSON returns the raw JSON of all resources of the CRD DiscoverCRD found.
	ListJSON(ctx context.Context, crd CRDDiscovery, namespace string) ([]byte, error)
}

// CRDDiscovery holds the result of discovering the Pacto CRD on the cluster.
type CRDDiscovery struct {
	Found        bool
	Version      string // preferred or first version
	ResourceName string // never empty when Found
}

// builders are the client-go constructors a client is assembled from. The
// exported constructors use defaultBuilders; a test builds its own to exercise
// the construction error paths without a real cluster.
type builders struct {
	inCluster func() (*rest.Config, error)
	discovery func(*rest.Config) (discovery.DiscoveryInterface, error)
	dynamic   func(*rest.Config) (dynamic.Interface, error)
}

func defaultBuilders() builders {
	return builders{
		inCluster: rest.InClusterConfig,
		discovery: func(c *rest.Config) (discovery.DiscoveryInterface, error) {
			return discovery.NewDiscoveryClientForConfig(c)
		},
		dynamic: func(c *rest.Config) (dynamic.Interface, error) {
			return dynamic.NewForConfig(c)
		},
	}
}

// CurrentKubeContext reads the current context name from the kubeconfig file,
// returning "" when it cannot be determined.
func CurrentKubeContext() string {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := loadingRules.Load()
	if err != nil {
		return ""
	}
	return config.CurrentContext
}

// k8sGoClient implements K8sClient using k8s.io/client-go.
type k8sGoClient struct {
	discovery discovery.DiscoveryInterface
	dynamic   dynamic.Interface
}

// NewGoClient constructs a cluster-backed K8sClient from in-cluster config or
// the ambient kubeconfig.
func NewGoClient() (K8sClient, error) {
	return defaultBuilders().goClient()
}

func (b builders) goClient() (K8sClient, error) {
	config, err := b.config()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	config.Timeout = 5 * time.Second

	disc, err := b.discovery(config)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client: %w", err)
	}

	dyn, err := b.dynamic(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &k8sGoClient{
		discovery: disc,
		dynamic:   dyn,
	}, nil
}

func (b builders) config() (*rest.Config, error) {
	// Try in-cluster config first.
	config, err := b.inCluster()
	if err != nil {
		// Fall back to kubeconfig.
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
		if err != nil {
			return nil, err
		}
	}
	// Server deprecation warnings go to klog, which writes to stderr directly and
	// so escapes every writer the caller controls. Nothing here reads them, and
	// under a full-screen TUI they corrupt the frame. Suppress per config rather
	// than mutating the client-go process global.
	config.WarningHandlerWithContext = rest.NoWarnings{}
	return config, nil
}

func (c *k8sGoClient) DiscoverCRD(ctx context.Context) (*CRDDiscovery, error) {
	result := &CRDDiscovery{}

	groups, _, err := c.discovery.ServerGroupsAndResources()
	if err != nil {
		return result, fmt.Errorf("listing API groups: %w", err)
	}

	for _, g := range groups {
		if g.Name != PactoGroup {
			continue
		}
		result.Found = true
		if g.PreferredVersion.Version != "" {
			result.Version = g.PreferredVersion.Version
		} else if len(g.Versions) > 0 {
			result.Version = g.Versions[0].Version
		}
		break
	}

	if !result.Found {
		return result, nil
	}

	// Discover the resource name for the CRD.
	gv := PactoGroup + "/" + result.Version
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

	return result, nil
}

func (c *k8sGoClient) ListJSON(ctx context.Context, crd CRDDiscovery, namespace string) ([]byte, error) {
	gvr := schema.GroupVersionResource{Group: PactoGroup, Version: crd.Version, Resource: crd.ResourceName}
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

// NewDynamic returns a dynamic client from in-cluster config or the ambient
// kubeconfig, for writers such as the `pacto agent` Kubernetes sink.
func NewDynamic() (dynamic.Interface, error) {
	return defaultBuilders().newDynamic()
}

func (b builders) newDynamic() (dynamic.Interface, error) {
	config, err := b.config()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	return b.dynamic(config)
}

// Config is the cluster config from in-cluster config or the ambient
// kubeconfig, with the process's own credentials.
func Config() (*rest.Config, error) {
	return defaultBuilders().config()
}

// Served reports whether the cluster serves the API group version gv,
// asked with config's own credentials through discovery. Only a NotFound
// means not served; any other failure is an error, so an unreachable
// cluster is never mistaken for one without the API.
func Served(config *rest.Config, gv string) (bool, error) {
	return defaultBuilders().served(config, gv)
}

func (b builders) served(config *rest.Config, gv string) (bool, error) {
	d, err := b.discovery(config)
	if err != nil {
		return false, err
	}
	switch _, err = d.ServerResourcesForGroupVersion(gv); {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}
