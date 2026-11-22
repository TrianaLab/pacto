/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

func TestForOperatorNilWithoutDeploymentName(t *testing.T) {
	if d := ForOperator("", opNS, nil, "", kubecluster.Identity{}, nil, nil, servedAll, nil, nil, Readers{GatewayLogs: "auto"}); d != nil {
		t.Errorf("detector = %+v, want nil", d)
	}
}

func TestForOperatorNamesAndSkips(t *testing.T) {
	d := ForOperator(deploy, opNS, []string{"a"}, "agents", kubecluster.Identity{ID: "uid", Name: "prod"}, nil, nil, servedAll, nil, nil, Readers{GatewayLogs: "on", KernelEvidence: true})
	if d.Readers != (Readers{GatewayLogs: "on", KernelEvidence: true}) || d.Name != cmName || d.DeploymentName != deploy || d.Namespace != opNS || d.Cluster != "uid" || d.ClusterName != "prod" || !slices.Equal(d.Namespaces, []string{"a"}) || d.Now == nil {
		t.Errorf("detector = %+v", d)
	}
	if want := []string{"kube-system", "kube-public", "kube-node-lease", opNS, "agents"}; !slices.Equal(d.Skip, want) {
		t.Errorf("skip = %v", d.Skip)
	}
}
