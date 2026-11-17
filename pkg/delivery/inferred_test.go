package delivery

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// withInferredLegacy rebuilds the plan's snapshot from the same Pacto and
// sides plus a workload source that sees legacy, so legacy is an inferred
// service as well as an uncontracted caller.
func withInferredLegacy(t *testing.T, in PlanInput, p PactoView) PlanInput {
	t.Helper()
	rev, digest := revisionFromStatus(p)
	reconciled := parseTime(p.Status.LastReconciledAt)
	col := &fleet.Collection{Observed: ObservedEdges(p)}
	for _, side := range []Revision{in.From, in.To} {
		repo, d, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef),
			RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + d, Digest: d})
	}
	col.Revisions = append(col.Revisions, rev)
	col.Targets = []fleet.RawTarget{{Scope: p.Metadata.Namespace, Kind: fleet.SourceKindKubernetes, Name: p.Metadata.Name,
		Service: p.Status.Contract.ServiceName, Domain: rev.Domain, ResolvedRef: p.Status.Contract.ResolvedRef, Digest: digest,
		Compliance: p.Status.ContractStatus, EvidenceAt: reconciled, ReconciledAt: reconciled}}
	workloads := &fleet.Collection{InferredServices: []fleet.RawInferredService{{Namespace: "prod", Kind: "Deployment", Name: "legacy"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col), fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, workloads))
	if err != nil {
		t.Fatal(err)
	}
	if s := snap.Services[fleet.NewServiceKey("k8s:prod/Deployment/legacy")]; s == nil || !s.Inferred() {
		t.Fatalf("legacy is not inferred: %v", snap.Services)
	}
	if c := snap.UncontractedCallers; len(c) != 1 || c[0].Entity != fleet.NewServiceKey("k8s:prod/Deployment/legacy") {
		t.Fatalf("legacy is no uncontracted caller linked to its inferred service: %+v", c)
	}
	in.Snapshot = snap
	return in
}

func TestInferredServiceNeverClearsGate(t *testing.T) {
	p := paymentsPacto(complete(), legacy)
	from, to := minorSides()
	plan := PlanFor(context.Background(), withInferredLegacy(t, planIn(t, from, to, p), p))
	if v := verdict(t, plan, GateImpact); v.Citation["uncontracted"] != "1" {
		t.Fatalf("the inferred caller is still cited uncontracted: %+v", v)
	}
	if got := ProposeLevel(StageSpec{}, StageStatus{Level: LevelAutoNonBreaking}, &plan); !slices.Contains(kinds(got), BlockerMissingContract) {
		t.Fatalf("missing-contract still blocks autonomous: %+v", got)
	}
	from, to = breakingSides()
	plan = PlanFor(context.Background(), withInferredLegacy(t, planIn(t, from, to, p), p))
	if v := verdict(t, plan, GateImpact); v.Result != Unknown || plan.Floor.Decision != AcceptUnknown {
		t.Fatalf("an inferred caller must hold a breaking change: %+v", plan)
	}
}
