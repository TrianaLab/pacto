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

// A recorded native registry mode never narrows: every reader reads both
// forms, but a stream that moved from referrers to write-once tags would hide
// its history's form change, so admission refuses it for everyone, the
// recorder included. The recorder alone sets the mode from empty and may
// widen write-once-tags to native.
func TestRecordsPolicyPinsNativeRegistryMode(t *testing.T) {
	const pin = "status.stream.registryMode is native and may not narrow to write-once-tags"
	// Others are refused by the promoter's status policy or the records one.
	const others = "denied request"
	mode := func(m string) map[string]any { return map[string]any{"stream": map[string]any{"registryMode": m}} }

	widened := mustCreate(t, admin, stage(uniq("payments")))
	denied(t, setStatus(as("alice"), widened, mode("write-once-tags")), others)
	allowed(t, setStatus(as(recorder), widened, mode("write-once-tags")))
	denied(t, setStatus(as("alice"), widened, mode("native")), others)
	allowed(t, setStatus(as(recorder), widened, mode("native")))
	denied(t, setStatus(as(recorder), widened, mode("write-once-tags")), pin)
	denied(t, setStatus(as("alice"), widened, mode("write-once-tags")), others)
	denied(t, setStatus(as(recorder), widened, map[string]any{"stream": map[string]any{}}), pin)

	native := mustCreate(t, admin, stage(uniq("payments")))
	allowed(t, setStatus(as(recorder), native, mode("native")))
	denied(t, setStatus(as(recorder), native, mode("write-once-tags")), pin)
}
