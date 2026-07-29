package dashboard

import "github.com/trianalab/pacto/v3/internal/k8sclient"

// K8sClient and CRDDiscovery are re-exported from internal/k8sclient so the
// dashboard's cluster-facing code keeps its local names while the concrete
// client is shared with the fleet's live Kubernetes source. The shared package
// keeps both consumers off a private client and away from the platform-neutral
// core.
type (
	K8sClient    = k8sclient.K8sClient
	CRDDiscovery = k8sclient.CRDDiscovery
)

// Constructor seams, overridable in tests to avoid real cluster access.
var (
	newK8sClientFunc       = k8sclient.NewGoClient
	currentKubeContextFunc = k8sclient.CurrentKubeContext
)
