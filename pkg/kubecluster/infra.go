package kubecluster

import (
	"errors"
	"slices"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// infraCategories are the API discovery categories Crossplane gives the
// kinds it manages: claims, composite resources and every provider's managed
// resources.
var infraCategories = []string{"claim", "composite", "managed"}

// Infra says whether kind of group is infrastructure Crossplane manages, from
// dc's discovery categories, and whether it could tell: a kind discovery does
// not list, or a group one of whose versions failed to list, is unknown.
// Every manifest inspector with a cluster (the promoter, the CLI, the MCP
// server and the dashboard) reads it here. Pass a cached client: every call
// lists the cluster's resources. A cached client is invalidated once when the
// kind is missing, so a kind served after it filled is read, never taken for
// "not infrastructure".
func Infra(dc discovery.DiscoveryInterface) func(group, kind string) (infra, known bool) {
	return func(group, kind string) (bool, bool) {
		infra, known := infraOf(dc, group, kind)
		if c, ok := dc.(discovery.CachedDiscoveryInterface); ok && !known {
			c.Invalidate()
			infra, known = infraOf(dc, group, kind)
		}
		return infra, known
	}
}

func infraOf(dc discovery.DiscoveryInterface, group, kind string) (infra, known bool) {
	_, lists, err := dc.ServerGroupsAndResources()
	for _, l := range lists {
		if gv, _ := schema.ParseGroupVersion(l.GroupVersion); gv.Group != group {
			continue
		}
		for _, r := range l.APIResources {
			if r.Kind != kind {
				continue
			}
			if slices.ContainsFunc(r.Categories, func(c string) bool { return slices.Contains(infraCategories, c) }) {
				return true, true
			}
			known = true
		}
	}
	// A version of the group that failed to list may give the kind its
	// categories.
	if failed := (*discovery.ErrGroupDiscoveryFailed)(nil); errors.As(err, &failed) {
		for gv := range failed.Groups {
			if gv.Group == group {
				return false, false
			}
		}
	}
	return false, known
}
