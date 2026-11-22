package app

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// DiscoveryScope is the scope of every kind the cluster cfg names serves,
// from API discovery, read lazily and cached for the life of the process:
// build it once per process, never per request. A config no discovery
// client can be built from, and no config, knows only the built-in kinds.
func DiscoveryScope(cfg *rest.Config) delivery.ScopeFunc {
	if cfg == nil {
		return delivery.BuiltinScope
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return delivery.BuiltinScope
	}
	return mapperScope(restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc)))
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
