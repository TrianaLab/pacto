package kubecluster

import (
	"slices"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// infraCategories are the API discovery categories Crossplane gives the
// kinds it manages: claims, composite resources and every provider's managed
// resources.
var infraCategories = []string{"claim", "composite", "managed"}

// Infra says whether kind of group is infrastructure Crossplane manages, from
// dc's discovery categories, and whether it could tell: a group discovery
// could not list is unknown. Every manifest inspector with a cluster (the
// promoter, the CLI, the MCP server and the dashboard) reads it here. Pass a
// cached client: every call lists the cluster's resources.
func Infra(dc discovery.DiscoveryInterface) func(group, kind string) (infra, known bool) {
	return func(group, kind string) (bool, bool) {
		_, lists, _ := dc.ServerGroupsAndResources()
		listed := false
		for _, l := range lists {
			if gv, _ := schema.ParseGroupVersion(l.GroupVersion); gv.Group != group {
				continue
			}
			listed = true
			for _, r := range l.APIResources {
				if r.Kind == kind && slices.ContainsFunc(r.Categories, func(c string) bool { return slices.Contains(infraCategories, c) }) {
					return true, true
				}
			}
		}
		return false, listed
	}
}
