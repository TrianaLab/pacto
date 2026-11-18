/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
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
