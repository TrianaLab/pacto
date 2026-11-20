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

// recorderGroups are the groups the apiserver gives the recorder's service
// account.
var recorderGroups = []string{"system:serviceaccounts", "system:serviceaccounts:pacto-delivery", "system:authenticated"}

// view is the Approval the recorder creates for a keyless approval record:
// the person the certificate names, no groups, the record and its verified
// signing time.
func view() *unstructured.Unstructured {
	return obj("delivery.pacto.trianalab.io/v1alpha1", "Approval", uniq("view"), nil, map[string]any{
		"operation": "p1", "operationUID": "0f6c2a1e-5b7d-4c3a-9e2f-1a2b3c4d5e6f", "intentDigest": digestA,
		"decision": "approve", "by": "oidc:alice@acme.com", "record": "reg/approvals@" + digestB, "signedAt": "2026-11-18T10:00:00Z",
	})
}

// TestApprovalAdmissionRecorderNeedsRecord: the recorder is a service
// account, so an Approval it creates that is not a view is an agent's.
func TestApprovalAdmissionRecorderNeedsRecord(t *testing.T) {
	err := as(recorder, recorderGroups...).Create(t.Context(), approval(recorder, recorderGroups[:2]...))
	denied(t, err, "agents cannot approve")
}

// TestApprovalAdmissionRecorderViewPassesGroupsAndAuthTime: the recorder's
// view names someone else, has no groups and no login time, and passes.
func TestApprovalAdmissionRecorderViewPassesGroupsAndAuthTime(t *testing.T) {
	allowed(t, as(recorder, recorderGroups...).Create(t.Context(), view()))
	v := view()
	v.Object["spec"].(map[string]any)["authTime"] = "2026-11-18T09:59:00Z"
	allowed(t, as(recorder, recorderGroups...).Create(t.Context(), v))
}

// TestApprovalAdmissionPersonCannotSetRecord: a person never sets
// spec.record or spec.signedAt, so nobody passes a record off as checked by
// the recorder.
func TestApprovalAdmissionPersonCannotSetRecord(t *testing.T) {
	const msg = "spec.record and spec.signedAt are set only by the Pacto delivery recorder"
	for _, field := range []string{"record", "signedAt"} {
		a := approval("dana@example.com")
		a.Object["spec"].(map[string]any)[field] = view().Object["spec"].(map[string]any)[field]
		denied(t, as("dana@example.com", "system:authenticated").Create(t.Context(), a), msg)
	}
	v := view()
	v.Object["spec"].(map[string]any)["by"] = "dana@example.com"
	denied(t, as("dana@example.com", "system:authenticated").Create(t.Context(), v), msg)
}
