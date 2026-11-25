/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package scope turns the operator's watched-namespace flags into the list the
// rest of the operator reads, and into the manager's cache options. It lives
// outside cmd/ so the operator's coverage gate covers it.
package scope

import (
	"slices"
	"strings"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/observer"
)

// Namespaces merges --watch-namespaces (comma-separated) with the deprecated
// --watch-namespace into one sorted list without duplicates. Nil means the
// operator watches the whole cluster.
func Namespaces(list, legacy string) []string {
	var out []string
	for s := range strings.SplitSeq(list+","+legacy, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// CacheOptions scopes the manager's cache. Pods are cached only for the kernel merge, which reads a handful
// of their fields: the transform drops the rest (managedFields included) before they reach the cache. The
// node agent's observations and heartbeat Leases are read in the kernel evidence namespace only, whatever
// the watched list says: a cluster-wide Lease informer would cache every controller's leader election.
// A non-empty namespaces list restricts everything else to those namespaces.
func CacheOptions(namespaces []string, kernelNamespace string, kernelEvidence bool) cache.Options {
	opts := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Transform: observer.TrimPod},
		},
	}
	// A ByObject entry makes the manager resolve the kind at startup, so registering the
	// node agent kinds only with kernel evidence on keeps an operator that has it off
	// starting even where the NodeRuntimeObservation CRD was never applied (helm upgrade
	// installs no new CRDs).
	if kernelEvidence {
		kernel := cache.ByObject{Namespaces: map[string]cache.Config{kernelNamespace: {}}}
		opts.ByObject[&pactov1.NodeRuntimeObservation{}] = kernel
		opts.ByObject[&coordinationv1.Lease{}] = kernel
	}
	if len(namespaces) > 0 {
		opts.DefaultNamespaces = make(map[string]cache.Config, len(namespaces))
		for _, ns := range namespaces {
			opts.DefaultNamespaces[ns] = cache.Config{}
		}
	}
	return opts
}
