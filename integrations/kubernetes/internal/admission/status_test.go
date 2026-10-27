/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func stage(name string) *unstructured.Unstructured {
	return obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", name, nil, map[string]any{
		"service": map[string]any{"name": "payments"}, "environment": "prod",
		"applyRef": map[string]any{"name": "payments"}})
}

// What a Stage or an Operation says happened is the promoter's to write:
// nobody RBAC lets patch the status raises a level, publishes a digest or
// edits an approval. A person may only clear upstreamSeen.
func TestStatusIsThePromoters(t *testing.T) {
	ctx := context.Background()
	const msg = "written only by the Pacto delivery promoter"
	st := mustCreate(t, admin, stage(uniq("payments")))
	allowed(t, setStatus(as(controller), st, map[string]any{"level": "advise", "pointer": digestA,
		"upstreamSeen": map[string]any{"stream": "s", "seq": int64(3), "record": digestA}}))
	for name, edit := range map[string]func(map[string]any){
		"level":     func(s map[string]any) { s["level"] = "autonomous" },
		"published": func(s map[string]any) { s["published"] = []any{digestB} },
		"frozen":    func(s map[string]any) { s["frozen"] = false; delete(s, "upstreamSeen") },
	} {
		t.Run(name, func(t *testing.T) {
			cur := st.DeepCopy()
			allowed(t, admin.Get(ctx, client.ObjectKeyFromObject(st), cur))
			edit(cur.Object["status"].(map[string]any))
			denied(t, as("alice").Status().Update(ctx, cur), msg)
		})
	}
	cur := st.DeepCopy()
	allowed(t, admin.Get(ctx, client.ObjectKeyFromObject(st), cur))
	delete(cur.Object["status"].(map[string]any), "upstreamSeen")
	allowed(t, as("alice").Status().Update(ctx, cur))
	// The recorder writes its record fields; the records policy holds it there.
	cur.Object["status"].(map[string]any)["stream"] = map[string]any{"head": digestB}
	allowed(t, as(recorder).Status().Update(ctx, cur))

	op := mustCreate(t, as("alice"), operation(uniq("op"), "alice", nil))
	allowed(t, setStatus(as(controller), op, map[string]any{"phase": "Held"}))
	denied(t, setStatus(as("alice"), op, map[string]any{"phase": "Held",
		"approvals": []any{map[string]any{"name": "a", "by": "bob", "decision": "Approve"}}}), msg)
}
