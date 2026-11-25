/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestOwnWorkloadNeverRefusesItsNamespace: the service's own workload, here
// the one its contract was inferred from, is the service's and not a
// workload of an unknown owner, so a namespace only it occupies places it
// with no Pacto beside it.
func TestOwnWorkloadNeverRefusesItsNamespace(t *testing.T) {
	rec := []byte("format: " + contract.InferredRecordFormat + "\ninputs: {}\nfields: []\ndeclaration:\n  state: declared\n" +
		"  asker: {subject: ana@acme.com, issuer: https://accounts.google.com, class: human}\n  inferredFrom: k8s:payments-staging/Deployment/api\n")
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("payments-staging"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "api",
			Labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "api", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}}})
	pe.publish(t, "payments", delivered("payments"), fstest.MapFS{contract.InferredRecordFile: {Data: rec}})
	pe.pass(t)
	p := rowOf(t, pe.rows(t), "payments", "staging").Placement
	if !p.Placed() || p.Namespace != "payments-staging" || p.Provenance != delivery.PlacementObserved || p.Source != delivery.PlacementFromInferred {
		t.Fatalf("placement %+v", p)
	}
}

// TestDomainlessHandWrittenStageNeverDuplicated: a hand-written Stage that
// names no domain (as pacto init --delivery wrote them) is the service's in
// any domain, so Pacto creates no second Stage beside it and chains the next
// environment to it.
func TestDomainlessHandWrittenStageNeverDuplicated(t *testing.T) {
	pe := twoRungs(t, nil)
	hand := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "payments"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: "payments"}, Environment: "staging"}}
	if err := pe.c.Create(t.Context(), hand); err != nil {
		t.Fatal(err)
	}
	v := pe.pass(t)
	if pe.has(t, &dv1.Stage{}, "payments-staging", "payments-staging") {
		t.Fatal("a second Stage beside the hand-written one")
	}
	row := rowOf(t, pe.rows(t), "payments", "staging")
	if row.Placement == nil || row.Placement.Source != delivery.PlacementFromStage || row.Stage != "payments-staging/payments" {
		t.Fatalf("row %+v", row)
	}
	prod := pe.stage(t, "payments-production", "payments-production")
	if up := prod.Spec.Candidates.UpstreamStage; up == nil || *up != (delivery.StageRef{Name: "payments", Namespace: "payments-staging"}) {
		t.Fatalf("production %+v", prod.Spec.Candidates)
	}
	// Both environments have a Stage: nothing is counted unplaced.
	if v.UnplacedCount != 0 {
		t.Fatalf("view %+v", v)
	}
}

// TestUnreadPactosNeverFallThrough: Pactos that could not be listed are
// unknown, never absent, so no later step (the default namespace here)
// places the service meanwhile.
func TestUnreadPactosNeverFallThrough(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if l.GetObjectKind().GroupVersionKind().Kind == "PactoList" {
			return errors.New("timeout")
		}
		return c.List(ctx, l, opts...)
	}}, namespace("staging"), namespace("production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	v := pe.pass(t)
	for _, r := range pe.rows(t) {
		if r.Placement == nil || r.Placement.Placed() || !strings.HasPrefix(r.Placement.Evidence, "the Pactos could not be listed") {
			t.Fatalf("row %+v", r)
		}
	}
	if v.Completeness != fleet.CompletenessPartial || v.UnplacedCount != 2 {
		t.Fatalf("view %+v", v)
	}
}

// TestPublisherCheckFailureIsUnread: a publisher check that could not run
// (the bundle repository cannot be listed) leaves the answer partial and
// says so, never "not trusted yet".
func TestPublisherCheckFailureIsUnread(t *testing.T) {
	pe := newPlaceEnv(t, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.publish(t, "payments", nil, nil)
	pe.r.Accepted = func(context.Context, delivery.ServiceRef, string) (*delivery.PublisherVerdict, string, error) {
		return nil, "", errors.New("503")
	}
	v := pe.pass(t)
	rows := pe.rows(t)
	if len(rows) != 1 || rows[0].Reason != "the publisher check could not run: 503" {
		t.Fatalf("rows %+v", rows)
	}
	if !pe.missedHas(t, v, "the publisher check of "+pe.root+"/payments: 503") {
		t.Fatalf("view %+v", v)
	}
}

// TestUnplacedManagedStageShowsUnknown: a managed Stage whose placement no
// longer places it (here the placement policy became unreadable) stops
// showing the placement Pacto wrote: its annotations read unknown.
func TestUnplacedManagedStageShowsUnknown(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return nil, "the placement policy x could not be read (503)"
	}
	pe.pass(t)
	a := pe.stage(t, "payments-staging", "payments-staging").Annotations
	if a["delivery.pacto.trianalab.io/placement-provenance"] != delivery.PlacementUnknown ||
		!strings.HasPrefix(a["delivery.pacto.trianalab.io/placement-evidence"], "the placement policy could not be read") {
		t.Fatalf("annotations %+v", a)
	}
	// A patch that fails is missed, privately.
	pe = twoRungs(t, nil)
	pe.pass(t)
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
		if _, ok := o.(*dv1.Stage); ok {
			return errors.New("denied")
		}
		return c.Patch(ctx, o, p, opts...)
	}})
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return nil, "the placement policy y could not be read (503)"
	}
	if v := pe.pass(t); !pe.missedHas(t, v, "the Stage payments-staging/payments-staging could not be updated: denied") {
		t.Fatalf("view %+v", v)
	}
}
