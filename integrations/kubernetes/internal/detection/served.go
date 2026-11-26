/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"errors"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// ServedFor asks discovery, with cfg's own credentials, whether a group
// version is served. Only NotFound means not served; any other failure is
// returned, so an unreachable API server is never read as one without the API.
func ServedFor(cfg *rest.Config) func(gv string) (bool, error) {
	return func(gv string) (bool, error) {
		d, err := discovery.NewDiscoveryClientForConfig(cfg)
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
}

// ManagedFor asks discovery, with cfg's own credentials, which managed kinds
// the default catalog names the cluster serves, at each group's preferred
// version: the resources by group version, list-capable kinds only. A
// failure to discover a catalog group is an error, so a group that did not
// answer never reads as one the cluster lacks; another group's failure is
// ignored.
func ManagedFor(cfg *rest.Config) func(context.Context) (map[string][]metav1.APIResource, error) {
	kinds := catalogKinds()
	return func(context.Context) (map[string][]metav1.APIResource, error) {
		d, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			return nil, err
		}
		lists, err := d.ServerPreferredResources()
		if failed, ok := errors.AsType[*discovery.ErrGroupDiscoveryFailed](err); ok {
			for gv := range failed.Groups {
				if kinds[gv.Group] {
					return nil, err
				}
			}
		} else if err != nil {
			return nil, err
		}
		return catalogResources(lists, kinds), nil
	}
}

// catalogKinds are the "<group>/<Kind>" rows of the default catalog's
// managed kinds and their groups: the only kinds the chart grants and the
// detector lists.
func catalogKinds() map[string]bool {
	f, _ := externalcatalog.ParseFile(externalcatalog.DefaultBytes()) // ponytail: TestDefaultCatalogValid
	kinds := map[string]bool{}
	for _, e := range f.Entries {
		for _, k := range e.Crossplane {
			group, _, _ := strings.Cut(k, "/")
			kinds[k], kinds[group] = true, true
		}
	}
	return kinds
}

// catalogResources are the list-capable resources of lists whose
// <group>/<Kind> kinds names, by group version.
func catalogResources(lists []*metav1.APIResourceList, kinds map[string]bool) map[string][]metav1.APIResource {
	out := map[string][]metav1.APIResource{}
	for _, l := range lists {
		group, _, _ := strings.Cut(l.GroupVersion, "/")
		for _, r := range l.APIResources {
			if kinds[group+"/"+r.Kind] && !strings.Contains(r.Name, "/") && slices.Contains(r.Verbs, "list") {
				out[l.GroupVersion] = append(out[l.GroupVersion], r)
			}
		}
	}
	return out
}
