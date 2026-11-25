package app

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestTargetSnapshotEmitsOneKernelEgressRow: the operator-side snapshot of
// two Pactos with an egress coverage reports one kernel-egress row.
func TestTargetSnapshotEmitsOneKernelEgressRow(t *testing.T) {
	t.Parallel()
	e := newSnapEnv(t)
	s := ledgerSnapshot(snapA, snapNow)
	cov := &delivery.EgressCoverageView{CallerCoverageView: delivery.CallerCoverageView{State: "partial", Reason: "node-without-agent", RefreshSeconds: 300}, Nodes: 4, NodesWithAgent: 3}
	ledger := s.Pactos[0]
	ledger.Status.StatusFormat, ledger.Status.ObservedEgressCoverage = delivery.StatusFormatEgress, cov
	mailer := ledger
	c := *ledger.Status.Contract
	c.ServiceName = "mailer"
	mailer.Metadata.Name, mailer.Status.Contract = "mailer", &c
	s.Pactos = []delivery.PactoView{ledger, mailer}
	e.push(t, e.key, snapA, s)
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	var rows []fleet.Provider
	for _, p := range snap.Providers {
		if p.ID == fleet.ProviderKernelEgress {
			rows = append(rows, p)
		}
	}
	if len(rows) != 1 || rows[0].Evidence != "node agent on 3/4 nodes" || rows[0].Status != fleet.SourcePartial || len(rows[0].Services) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}
