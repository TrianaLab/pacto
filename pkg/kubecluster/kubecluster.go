// Package kubecluster reads which Kubernetes cluster a client talks to: the
// kube-system namespace UID, which is the cluster's identity, and a display
// name with the signal it came from. Every reader of a cluster identity (the
// CLI's live source, the operator manager and the delivery binary) goes
// through it, so the read and the name chain exist once.
package kubecluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Identity is a cluster's identity and display name. ID is the kube-system
// namespace UID; Name is display only and never an identity.
type Identity struct {
	ID         string
	Name       string
	NameSource string
}

// Where a cluster's display name came from.
const (
	// NameFromNodeLabel: Identify read a node label whose value names the
	// cluster.
	NameFromNodeLabel = "node-label"
	// NameFromContext: Identify fell back to the kubeconfig context name.
	NameFromContext = "context"
	// NameFromUID: Identify fell back to the first characters of the UID.
	NameFromUID = "uid"
)

var (
	namespaces = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	nodes      = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
)

// ID reads the cluster id: the kube-system namespace UID.
func ID(ctx context.Context, dyn dynamic.Interface) (string, error) {
	obj, err := dyn.Resource(namespaces).Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading the cluster id: %w", err)
	}
	if obj.GetUID() == "" {
		return "", errors.New("reading the cluster id: the kube-system namespace has no uid")
	}
	return string(obj.GetUID()), nil
}

// Identify reads the cluster id and a display name: a node label that names
// the cluster, else contextName, else the first 8 characters of the UID.
func Identify(ctx context.Context, dyn dynamic.Interface, contextName string) (Identity, error) {
	id, err := ID(ctx, dyn)
	if err != nil {
		return Identity{}, err
	}
	if name, ok := nameFromNodes(ctx, dyn); ok {
		return Identity{ID: id, Name: name, NameSource: NameFromNodeLabel}, nil
	}
	if contextName != "" {
		return Identity{ID: id, Name: contextName, NameSource: NameFromContext}, nil
	}
	return Identity{ID: id, Name: id[:min(8, len(id))], NameSource: NameFromUID}, nil
}

// nodeNameLabels are the node labels whose value names the cluster, checked
// against provider documentation: eksctl sets alpha.eksctl.io/cluster-name to
// the cluster name (setDefaultNodeLabels); AKS sets kubernetes.azure.com/cluster
// to the node resource group, MC_<group>_<cluster>_<location> by default. GKE
// has no such label. One more provider is one row.
var nodeNameLabels = []struct {
	label string
	parse func(string) (string, bool)
}{
	{"alpha.eksctl.io/cluster-name", nonEmpty},
	{"kubernetes.azure.com/cluster", aksClusterName},
}

// nameFromNodes reads the first node's labels. Any error, Forbidden included,
// means no name: in-cluster processes usually cannot list nodes.
func nameFromNodes(ctx context.Context, dyn dynamic.Interface) (string, bool) {
	list, err := dyn.Resource(nodes).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(list.Items) == 0 {
		return "", false
	}
	labels := list.Items[0].GetLabels()
	for _, row := range nodeNameLabels {
		if v, ok := labels[row.label]; ok {
			if name, ok := row.parse(v); ok {
				return name, true
			}
		}
	}
	return "", false
}

func nonEmpty(v string) (string, bool) { return v, v != "" }

// aksClusterName accepts only the default node resource group shape with
// exactly four parts; a group name may hold "_", so any other shape is
// ambiguous and skipped.
func aksClusterName(v string) (string, bool) {
	parts := strings.Split(v, "_")
	if len(parts) != 4 || parts[0] != "MC" || slices.Contains(parts, "") {
		return "", false
	}
	return parts[2], true
}
