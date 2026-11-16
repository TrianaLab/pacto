/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// approval is an Approval by user in groups, plus the system:authenticated
// group the apiserver adds to every authenticated user.
func approval(user string, groups ...string) *unstructured.Unstructured {
	g := make([]any, 0, len(groups)+1)
	for _, x := range append(groups, "system:authenticated") {
		g = append(g, x)
	}
	return obj("delivery.pacto.trianalab.io/v1alpha1", "Approval", uniq("ok"), nil, map[string]any{
		"operation": "p1", "operationUID": "0f6c2a1e-5b7d-4c3a-9e2f-1a2b3c4d5e6f", "intentDigest": digestA,
		"decision": "approve", "by": user, "groups": g,
	})
}

// People and agents are told apart by how they sign in, with no identity
// values: a ServiceAccount, a system: control-plane user and an agentGroups
// member are agents, every other authenticated user is a person. Every user
// here holds cluster-admin, so only admission refuses; whether a person may
// update the workload is the promoter's review (internal/delivery).
func TestApprovalAdmissionClassifies(t *testing.T) {
	const agent = "agents cannot approve"
	for name, tc := range map[string]struct {
		user   string
		groups []string
		refuse bool
	}{
		"service account":       {user: "system:serviceaccount:ci:ci-bot", refuse: true},
		"system user":           {user: "system:kube-scheduler", refuse: true},
		"oidc person":           {user: "dana@example.com"},
		"agentGroups member":    {user: "bot@example.com", groups: []string{agents}, refuse: true},
		"person in no group":    {user: "erin@example.com"},
		"person in a team":      {user: "frank@example.com", groups: []string{"team-payments"}},
		"unauthenticated group": {user: "guest@example.com", groups: []string{"system:unauthenticated"}, refuse: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := as(tc.user, append(tc.groups, "system:authenticated")...).Create(t.Context(), approval(tc.user, tc.groups...))
			if tc.refuse {
				denied(t, err, agent)
				return
			}
			allowed(t, err)
		})
	}
}
