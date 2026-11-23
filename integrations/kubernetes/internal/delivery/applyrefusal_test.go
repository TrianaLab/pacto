/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"net/http"
	"strings"
	"testing"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// manifestsRejected is a concluded promote to digest that the manifest
// inspector rejected for a ClusterRoleBinding.
func (re *recEnv) manifestsRejected(name string, seq int64, digest string) *dv1.Operation {
	re.t.Helper()
	o := re.concluded(name, seq, delivery.Rejected)
	o.Spec.Params.To = digest
	must(re.t, re.c.Update(context.Background(), o))
	o.Status.Verdicts = []delivery.Verdict{{Gate: delivery.GateManifests, Phase: "pre", Result: delivery.Fail,
		Citation: map[string]string{"refused": "ClusterRoleBinding orders-admin is cluster-scoped"}, Message: "the version reaches outside prod"}}
	must(re.t, re.c.Status().Update(context.Background(), o))
	return o
}

// applyRefusals reads every apply refusal bundle on payments' stream, one
// per push, verified with the recorder's key.
func (re *recEnv) applyRefusals() []deliveryrecord.ApplyRefusal {
	re.t.Helper()
	repo, err := ocireferrer.Open(re.rec.Store.Streams, ocireferrer.Options{})
	must(re.t, err)
	svc := delivery.ServiceRef{Name: "payments"}
	fetched, _, err := (&deliveryrecord.Reader{}).ListPredicate(context.Background(), repo, delivery.ApplyRefusalStream(svc), delivery.PredicateApplyRefusal, "", 100)
	must(re.t, err)
	out := make([]deliveryrecord.ApplyRefusal, 0, len(fetched))
	for _, f := range fetched {
		st, id, err := delivery.OpenApplyRefusal(f.Bundle, func(b []byte) error { return re.rec.Store.Verify("", 0, b) })
		must(re.t, err)
		out = append(out, deliveryrecord.ApplyRefusal{ID: id, Refusal: st.Predicate})
	}
	return out
}

// TestRecorderWritesOneApplyRefusalPerDigest: a promote the inspector
// rejected leaves one signed refusal on the service's stream citing its
// record; a second Operation on the same digest, or a restart, pushes none;
// another digest pushes one.
func TestRecorderWritesOneApplyRefusalPerDigest(t *testing.T) {
	re := newRecEnv(t)
	re.stage(3)
	re.manifestsRejected("p1", 1, re.reg.to)
	re.manifestsRejected("p2", 2, re.reg.to)
	p3 := re.manifestsRejected("p3", 3, re.reg.from)
	re.reconcile()
	recs := re.records()
	got := re.applyRefusals()
	if len(recs) != 3 || len(got) != 2 {
		t.Fatalf("records %d, refusals %+v", len(recs), got)
	}
	byDigest := map[string]delivery.ApplyRefusal{}
	for _, r := range got {
		byDigest[r.Refusal.Digest] = r.Refusal
	}
	r := byDigest[re.reg.to]
	if r.Operation != "p1" || r.Stage != "payments-prod" || r.Namespace != ns || r.Record == "" || len(r.Refused) != 1 ||
		!strings.Contains(r.Refused[0], "orders-admin") || byDigest[re.reg.from].Operation != "p3" {
		t.Fatalf("refusals %+v", byDigest)
	}
	cited := false
	for _, s := range recs {
		cited = cited || s.ID == r.Record
	}
	if !cited {
		t.Fatalf("the refusal cites %s, no record of the stream", r.Record)
	}
	re.reconcile()
	// A restart forgets what it pushed; the stream still holds it.
	re.rec.refused = nil
	stage := &dv1.Stage{}
	must(t, re.c.Get(context.Background(), keyOf("payments-prod"), stage))
	w := &writer{Recorder: re.rec, stage: stage}
	must(t, w.applyRefusal(context.Background(), re.getOp(p3.Name), &recs[2]))
	if n := len(re.applyRefusals()); n != 2 {
		t.Fatalf("a second reconcile or a restart pushed again: %d", n)
	}
}

// TestApplyRefusalFailuresHoldTheRecord: a refusal that cannot be signed or
// written is an error, and the recorder does not release the operation.
func TestApplyRefusalFailuresHoldTheRecord(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	o := re.manifestsRejected("p1", 1, re.reg.to)
	anchor := deliveryrecord.AnchorTag(delivery.ApplyRefusalStream(delivery.ServiceRef{Name: "payments"}))
	re.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/manifests/"+anchor) }
	re.reconcile()
	if s := re.getStage(); condition(s).Status == statusTrue || re.getOp(o.Name).Status.Record != nil {
		t.Fatalf("an unwritable refusal stream released the operation: %+v", condition(s))
	}
	re.reg.fault = nil
	stage := &dv1.Stage{}
	must(t, re.c.Get(context.Background(), keyOf("payments-prod"), stage))
	w := &writer{Recorder: re.rec, stage: stage}
	rec := &stored{ID: "rec"}
	re.rec.Signer = failingSigner{}
	if err := w.applyRefusal(context.Background(), o, rec); err == nil {
		t.Fatal("an unsignable refusal was no error")
	}
	re.rec.Signer = edSigner(re.key)
	re.reg.fault = func(r *http.Request) bool { return r.Method == http.MethodPut }
	if err := w.applyRefusal(context.Background(), o, rec); err == nil {
		t.Fatal("an unpushable refusal was no error")
	}
	re.reg.fault = nil
	must(t, w.applyRefusal(context.Background(), o, rec))
	re.rec.refused = nil
	re.reg.fault = func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/manifests/sha256:")
	}
	if err := w.applyRefusal(context.Background(), o, rec); err == nil {
		t.Fatal("an unlistable refusal stream was no error")
	}
}
