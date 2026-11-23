package detect

import "time"

// ClustersConfigMapKey is the key of the operator's detected ConfigMap that
// holds the candidate clusters, beside [ConfigMapKey].
const ClustersConfigMapKey = "clusters.json"

// Bounds of a Clusters document: candidates kept, labels per candidate and
// the encoded size.
const (
	MaxCandidates      = 200
	MaxCandidateLabels = 16
	MaxClustersBytes   = 192 << 10
)

// The inventory sources a candidate can come from.
const (
	SourceArgoCD  = "argocd"
	SourceFlux    = "flux"
	SourceCAPI    = "capi"
	SourceOCM     = "ocm"
	SourceRancher = "rancher"
)

// inventoryKind is the detection entry kind that reports the inventory read.
const inventoryKind = "cluster-inventory"

// Clusters is one inventory read: the clusters the hub's GitOps and cluster
// managers know. Every candidate is inferred; nothing joins on its own.
type Clusters struct {
	SchemaVersion string    `json:"schemaVersion"`
	AsOf          time.Time `json:"asOf"`
	// Cluster is the id of the cluster the operator read.
	Cluster    string            `json:"cluster,omitempty"`
	Sources    []InventorySource `json:"sources"`
	Candidates []Candidate       `json:"candidates"`
	// Total is how many candidates were found before the list was cut.
	Total int `json:"total"`
}

// InventorySource is how one source was read: completeness, why it is short
// of complete and the namespaces it was read in.
type InventorySource struct {
	Source       string   `json:"source"`
	Completeness string   `json:"completeness"`
	Reason       string   `json:"reason,omitempty"`
	Namespaces   []string `json:"namespaces,omitempty"`
}

// Candidate is one cluster an inventory source knows. UID is set only when
// the source object states one, and UIDSource says where.
type Candidate struct {
	Source      string            `json:"source"`
	Kind        string            `json:"kind"`
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	UID         string            `json:"uid,omitempty"`
	UIDSource   string            `json:"uidSource,omitempty"`
	// KubeConfig is the kubeconfig Secret or ConfigMap a Flux object names.
	KubeConfig *Object `json:"kubeConfig,omitempty"`
}
