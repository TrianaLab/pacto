package fleetsrc

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var egressNow = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// egressView is service's Pacto at status format 4, reconciled at, with an
// egress coverage of state and reason (nil coverage when state is empty).
func egressView(service, state, reason string, nodes, withAgent int32, at time.Time) delivery.PactoView {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "prod", service
	p.Status.StatusFormat = delivery.StatusFormatEgress
	p.Status.LastReconciledAt = at.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: service, ResolvedRef: "oci://reg.example/pactos/" + service + "@sha256:" + strings.Repeat("a", 64)}
	if state != "" {
		p.Status.ObservedEgressCoverage = &delivery.EgressCoverageView{
			CallerCoverageView: delivery.CallerCoverageView{State: state, Reason: reason, RefreshSeconds: 300}, Nodes: nodes, NodesWithAgent: withAgent}
	}
	return p
}

func TestKernelEgressRow(t *testing.T) {
	older := egressView("ledger", "complete", "", 9, 9, egressNow.Add(-time.Minute))
	row, ok := KernelEgressRow("k8s", []delivery.PactoView{older, egressView("payments", "partial", "node-without-agent", 4, 3, egressNow)}, egressNow)
	if !ok || row.ID != fleet.ProviderKernelEgress || row.Name != "Kernel egress" || row.Evidence != "node agent on 3/4 nodes" ||
		row.Status != fleet.SourcePartial || !slices.Equal(row.UsedFor, []string{fleet.UseExternalEgress}) {
		t.Fatalf("row = %+v, %v", row, ok)
	}
	if len(row.Limitations) != 1 || row.Limitations[0].Code != fleet.LimitationExternalsUnreported || row.Limitations[0].Source != "k8s" ||
		!strings.Contains(row.Limitations[0].Message, "node-without-agent") {
		t.Fatalf("limitations = %+v", row.Limitations)
	}
}

func TestKernelEgressRowWorstFreshWins(t *testing.T) {
	// The worst fresh coverage wins, whatever its order.
	row, _ := KernelEgressRow("k8s", []delivery.PactoView{egressView("a", "unavailable", "pods-unreadable", 4, 3, egressNow), egressView("b", "partial", "truncated", 4, 3, egressNow)}, egressNow)
	if row.Status != fleet.SourceUnavailable || !strings.Contains(row.Limitations[0].Message, "pods-unreadable") {
		t.Fatalf("worst = %+v", row)
	}
	if row, _ := KernelEgressRow("k8s", []delivery.PactoView{egressView("a", "complete", "", 2, 2, egressNow)}, egressNow); row.Status != fleet.SourceAvailable || row.Limitations != nil {
		t.Fatalf("complete = %+v", row)
	}
	// A state the operator does not write is never read as complete.
	if row, _ := KernelEgressRow("k8s", []delivery.PactoView{egressView("a", "bogus", "", 2, 2, egressNow)}, egressNow); row.Status != fleet.SourceUnavailable {
		t.Fatalf("unknown state = %+v", row)
	}
}

func TestKernelEgressRowNoAgent(t *testing.T) {
	unreconciled := egressView("a", "complete", "", 2, 2, egressNow)
	unreconciled.Status.Contract = nil
	older := egressView("b", "complete", "", 2, 2, egressNow)
	older.Status.StatusFormat = delivery.StatusFormatExternals
	for _, views := range [][]delivery.PactoView{nil, {egressView("a", "", "", 0, 0, egressNow)}, {unreconciled}, {older}} {
		if row, ok := KernelEgressRow("k8s", views, egressNow); ok {
			t.Fatalf("row with the node agent off = %+v", row)
		}
	}
}

func TestKernelEgressRowAllStale(t *testing.T) {
	old := egressNow.Add(-time.Hour)
	stale := []delivery.PactoView{egressView("a", "complete", "", 2, 2, old), egressView("b", "partial", "truncated", 2, 2, old)}
	row, ok := KernelEgressRow("k8s", stale, egressNow)
	if !ok || row.Status != fleet.SourceStale || len(row.Limitations) != 1 || !strings.Contains(row.Limitations[0].Message, "stale-status") {
		t.Fatalf("row = %+v", row)
	}
	// One fresh coverage is enough: the stale ones are left out of the status.
	if row, _ := KernelEgressRow("k8s", append(stale, egressView("c", "complete", "", 2, 2, egressNow)), egressNow); row.Status != fleet.SourceAvailable {
		t.Fatalf("row = %+v", row)
	}
	// A status with no reconcile time is stale and still counts its nodes.
	untimed := egressView("a", "complete", "", 2, 2, egressNow)
	untimed.Status.LastReconciledAt = ""
	if row, _ := KernelEgressRow("k8s", []delivery.PactoView{untimed}, egressNow); row.Status != fleet.SourceStale || row.Evidence != "node agent on 2/2 nodes" {
		t.Fatalf("row = %+v", row)
	}
}

func TestKernelEgressRowServicesAndScope(t *testing.T) {
	row, _ := KernelEgressRow("k8s", []delivery.PactoView{egressView("payments", "complete", "", 2, 2, egressNow), egressView("mailer", "", "", 0, 0, egressNow),
		egressView("ledger", "complete", "", 2, 2, egressNow)}, egressNow)
	want := []fleet.ServiceKey{fleet.NewServiceKeyDomain("reg.example/pactos", "payments"), fleet.NewServiceKeyDomain("reg.example/pactos", "ledger")}
	if !reflect.DeepEqual(row.Services, want) || row.Scope != fleet.ScopeCluster {
		t.Fatalf("services %v scope %q", row.Services, row.Scope)
	}
}
