package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// clustersInstructions describe pacto_cluster_list; they are part of the
// fleet text only when the tool is registered.
const clustersInstructions = "pacto_cluster_list lists the member clusters that joined this hub through the registry: approved members, " +
	"pending clusters with why each is pending and removed members. Only an approved member's services, targets and workloads are in the fleet; " +
	"a pending or removed cluster is in no other answer. A member's environments are inferred and say where they came from. " +
	"Approving or removing a member is a person's signed decision with pacto cluster approve or remove: propose it to a person, never claim it is done."

// ClustersProvider lists the member clusters of this hub as the calling
// person may see them.
type ClustersProvider func(ctx context.Context) (delivery.ClusterJoinList, error)

func clusterListTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_cluster_list",
		Description: "List the member clusters that joined this hub through the registry, the pending ones and those removed, with each " +
			"member's state, display name and its source, advertised environments, key fingerprint and group mappings (issuer, rules, approvedBy, " +
			"approvedAt). Approving is a person's signed decision with `pacto cluster approve`; no tool approves. Group mappings are approved by a " +
			"person with `pacto cluster map-groups`; this server never writes them. Read-only. The answer carries asOf, completeness, limitations and " +
			"hidden, the clusters you may not see because you may not approve clusterjoins.",
		Annotations: readOnly(true),
		InputSchema: map[string]any{"type": "object"},
	}
}

// clusterListHandler answers pacto_cluster_list from p.
func clusterListHandler(p ClustersProvider) mcpsdk.ToolHandlerFor[map[string]any, any] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ map[string]any) (*mcpsdk.CallToolResult, any, error) {
		l, err := p(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(l)
		return r, nil, err
	}
}
