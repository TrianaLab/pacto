package fleet

import (
	"context"
	"testing"
	"time"
)

func TestTargetRunsAs(t *testing.T) {
	now := time.Now().UTC()
	snap, err := Build(context.Background(), BuildOptions{Now: func() time.Time { return now }}, NewMemorySource("local", "local", &Collection{
		Targets: []RawTarget{
			{Scope: "billing", Kind: SourceKindKubernetes, Name: "reports", Service: "reports", EvidenceAt: &now, Workload: &WorkloadRef{Kind: "Deployment", Name: "reports-api"}},
			{Scope: "billing", Kind: SourceKindKubernetes, Name: "ledger", Service: "ledger", EvidenceAt: &now},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if w := snap.Targets[NewTargetKey("billing", SourceKindKubernetes, "reports")].RunsAs(); w == nil || *w != (WorkloadRef{Kind: "Deployment", Name: "reports-api"}) {
		t.Errorf("reports runs as %+v", w)
	}
	if w := snap.Targets[NewTargetKey("billing", SourceKindKubernetes, "ledger")].RunsAs(); w != nil {
		t.Errorf("ledger runs as %+v", w)
	}
}
