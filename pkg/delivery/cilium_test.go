package delivery

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ciliumStripe is a Cilium toFQDNs rule that allows api.stripe.com.
var ciliumStripe = ExternalView{Identity: "stripe", Class: "vendor-api", Confidence: "likely", Kind: "ciliumFQDN",
	Where: "ciliumnetworkpolicy/prod/egress toFQDNs api.stripe.com", Host: "api.stripe.com", Entry: "stripe"}

// withCilium is p reporting only a Cilium toFQDNs use.
func withCilium(p PactoView) PactoView {
	p.Status.StatusFormat = StatusFormatExternals
	p.Status.Externals, p.Status.ExternalsCoverage = []ExternalView{ciliumStripe}, complete()
	p.Status.ExternalCatalog = externalcatalog.DefaultDigest() + " (embedded)"
	return p
}

// TestCiliumFQDNUseNeverClearsAGate: a Stage's gates (impact, environment
// and the rest) give the same verdicts, floor and question with and without
// a Cilium toFQDNs use on the provider and on its consumer, and the use
// reaches the fleet as an inferred edge only.
func TestCiliumFQDNUseNeverClearsAGate(t *testing.T) {
	old := bundle("payments", "1.0.0")
	prod := map[string]string{"prod.yaml": "app:\n  replicas: 3\n"}
	for _, c := range []struct {
		name     string
		from, to Revision
	}{
		{"breaking", rev(dA, c1, old), rev(dB, c2, bundle("payments", "2.0.0"))},
		{"environment", rev(dA, c1, old), rev(dB, c2, envBundle(map[string]string{"prod": "prod.yaml"}, prod, true))},
	} {
		plan := func(pactos ...PactoView) (Plan, *fleet.FleetSnapshot) {
			snap, err := Snapshot(context.Background(), pactos, nil, []Revision{c.from, c.to}, now)
			if err != nil {
				t.Fatal(err)
			}
			return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(),
				Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}, From: c.from, To: c.to, Snapshot: snap, Class: ClassAgent}), snap
		}
		without, _ := plan(checkoutPacto("^1.0.0"), paymentsPacto(complete()))
		with, snap := plan(withCilium(checkoutPacto("^1.0.0")), withCilium(paymentsPacto(complete())))
		// The snapshot holds the use, so its digest and the impact digest citing it differ; nothing else may.
		if !reflect.DeepEqual(undigested(without.Verdicts), undigested(with.Verdicts)) || !reflect.DeepEqual(without.Floor, with.Floor) ||
			without.Question != with.Question || without.Predicted != with.Predicted {
			t.Errorf("%s: without %+v\nwith %+v", c.name, without, with)
		}
		uses := 0
		for _, r := range snap.Relationships {
			if r.ToExternal != "stripe" {
				continue
			}
			if uses++; r.Provenance != fleet.ProvenanceInferred || r.Resolved || r.InferredSources[0].Kind != "ciliumFQDN" {
				t.Errorf("%s: a Cilium use is %+v", c.name, r)
			}
		}
		if uses != 2 {
			t.Errorf("%s: %d Cilium uses reach the fleet, want 2", c.name, uses)
		}
	}
}

// undigested is vs without the snapshot and impact digests they cite.
func undigested(vs []Verdict) []Verdict {
	out := make([]Verdict, 0, len(vs))
	for _, v := range vs {
		v.Citation = maps.Clone(v.Citation)
		delete(v.Citation, "snapshot")
		delete(v.Citation, "impact")
		out = append(out, v)
	}
	return out
}
