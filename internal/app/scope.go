package app

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// DiscoveryScope is the scope of every kind the cluster cfg names serves,
// and which of them are infrastructure Crossplane manages, from API
// discovery, read lazily and cached for the life of the process: build it
// once per process, never per request. A config no discovery client can be
// built from, and no config, knows only the built-in kinds and tells
// infrastructure by group only.
func DiscoveryScope(cfg *rest.Config) (delivery.ScopeFunc, delivery.InfraFunc) {
	if cfg == nil {
		return delivery.BuiltinScope, nil
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return delivery.BuiltinScope, nil
	}
	cached := memory.NewMemCacheClient(dc)
	return mapperScope(restmapper.NewDeferredDiscoveryRESTMapper(cached)), kubecluster.Infra(cached)
}

// mapperScope reads a kind's scope from m: a kind m cannot map is unknown.
func mapperScope(m meta.RESTMapper) delivery.ScopeFunc {
	return func(group, kind string) (bool, bool) {
		mapping, err := m.RESTMapping(schema.GroupKind{Group: group, Kind: kind})
		if err != nil {
			return false, false
		}
		return mapping.Scope.Name() == meta.RESTScopeNameNamespace, true
	}
}
