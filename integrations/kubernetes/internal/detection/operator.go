/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// ForOperator is the operator's Detector: it writes <deployName>-detected in
// namespace, owned by the Deployment deployName, and skips the system, operator
// and node agent namespaces when counting registries. It is nil when the
// operator runs outside the chart (no Deployment name), so nothing is written.
func ForOperator(deployName, namespace string, watched []string, nodeAgentNS string, ident kubecluster.Identity,
	r client.Reader, w client.Client, served func(string) (bool, error)) *Detector {
	if deployName == "" {
		return nil
	}
	return &Detector{
		Reader: r, Writer: w, Served: served,
		Namespace: namespace, Name: deployName + "-detected", DeploymentName: deployName,
		Namespaces: watched,
		Skip:       []string{"kube-system", "kube-public", "kube-node-lease", namespace, nodeAgentNS},
		Cluster:    ident.ID,
		Now:        time.Now,
	}
}
