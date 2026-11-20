// Package k8sclient is the shared, dashboard-independent Kubernetes access seam
// for reading Pacto CRD status from a cluster. It is consumed by both the
// dashboard and the fleet's live Kubernetes source, so neither owns a private
// client. It never appears in the platform-neutral core (see the architecture
// boundary test): it is an integration-side adapter that speaks client-go.
package k8sclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
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
	// ListPage returns one page (at most listPageSize items) of gvr in
	// namespace ("" for every namespace) as list JSON, starting at
	// continueToken, and the token of the next page ("" after the last).
	ListPage(ctx context.Context, gvr schema.GroupVersionResource, namespace, continueToken string) (data []byte, next string, err error)
	// ClusterIdentity reads the cluster's id and a display name; contextName is
	// the kubeconfig context, the name used when no node label names the cluster.
	ClusterIdentity(ctx context.Context, contextName string) (kubecluster.Identity, error)
	// PreferredVersions maps each of groups the cluster serves to the version
	// it prefers, in one discovery call. A group absent from the map is not
	// served.
	PreferredVersions(ctx context.Context, groups ...string) (map[string]string, error)
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
	return NewGoClientFor("")
}

// NewGoClientFor is [NewGoClient] for one kubeconfig context; "" is the
// current one.
func NewGoClientFor(context string) (K8sClient, error) {
	return defaultBuilders().goClient(context)
}

func (b builders) goClient(context string) (K8sClient, error) {
	config, err := b.config(context)
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

// config is the config of the kubeconfig context named context. "" tries
// in-cluster config first and falls back to the current context; a named
// context never reads in-cluster config, so a pod can read it too.
func (b builders) config(context string) (*rest.Config, error) {
	config, err := b.inClusterUnless(context)
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{CurrentContext: context}
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

// errNamedContext sends a named context straight to the kubeconfig.
var errNamedContext = errors.New("a named context reads the kubeconfig")

// inClusterUnless is the in-cluster config when context is "", else
// errNamedContext.
func (b builders) inClusterUnless(context string) (*rest.Config, error) {
	if context != "" {
		return nil, errNamedContext
	}
	return b.inCluster()
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
		result.Version = preferredVersion(g)
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

// preferredVersion is the version g prefers, else its first one.
func preferredVersion(g *metav1.APIGroup) string {
	if g.PreferredVersion.Version != "" || len(g.Versions) == 0 {
		return g.PreferredVersion.Version
	}
	return g.Versions[0].Version
}

func (c *k8sGoClient) PreferredVersions(_ context.Context, groups ...string) (map[string]string, error) {
	list, err := c.discovery.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("listing API groups: %w", err)
	}
	out := map[string]string{}
	for i := range list.Groups {
		if g := &list.Groups[i]; slices.Contains(groups, g.Name) && preferredVersion(g) != "" {
			out[g.Name] = preferredVersion(g)
		}
	}
	return out, nil
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

// listPageSize bounds the items one ListPage call holds in memory.
const listPageSize = 500

func (c *k8sGoClient) ListPage(ctx context.Context, gvr schema.GroupVersionResource, namespace, continueToken string) ([]byte, string, error) {
	list, err := c.dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: listPageSize, Continue: continueToken})
	if err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(list)
	return data, list.GetContinue(), err
}

func (c *k8sGoClient) ClusterIdentity(ctx context.Context, contextName string) (kubecluster.Identity, error) {
	return kubecluster.Identify(ctx, c.dynamic, contextName)
}

// NewDynamic returns a dynamic client from in-cluster config or the ambient
// kubeconfig, for writers such as the `pacto agent` Kubernetes sink.
func NewDynamic() (dynamic.Interface, error) {
	return defaultBuilders().newDynamic()
}

func (b builders) newDynamic() (dynamic.Interface, error) {
	config, err := b.config("")
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	return b.dynamic(config)
}

// Config is the cluster config from in-cluster config or the ambient
// kubeconfig, with the process's own credentials.
func Config() (*rest.Config, error) {
	return ConfigFor("")
}

// ConfigFor is [Config] for one kubeconfig context; "" is the current one.
func ConfigFor(context string) (*rest.Config, error) {
	return defaultBuilders().config(context)
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
