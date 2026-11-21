package fleetsrc

import (
	"fmt"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// egressRowStatus is the row status of an egress coverage state and how bad
// it is; a state the operator does not write reads unavailable.
func egressRowStatus(state string) (fleet.SourceStatus, int) {
	switch state {
	case "complete":
		return fleet.SourceAvailable, 0
	case "partial":
		return fleet.SourcePartial, 1
	}
	return fleet.SourceUnavailable, 2
}

// KernelEgressRow is the kernel-egress provider row of source over views,
// the Pactos one collection read: ok is false when none carries an egress
// coverage (the node agent is off). Its evidence is the node count of the
// most recently reconciled coverage, its status the worst fresh coverage
// (stale when every one is stale), with that coverage's reason as its one
// limitation. It names the contributing services, so a reader who sees none
// of them sees no row, and it is cluster-scoped, so a reader without the
// cluster scope sees it redacted. Build it once per collection, never per
// Pacto or namespace. It lives here, not in pkg/delivery, because no gate
// path may read provider rows.
func KernelEgressRow(source string, views []delivery.PactoView, now time.Time) (fleet.Provider, bool) {
	row := fleet.Provider{ID: fleet.ProviderKernelEgress, Name: "Kernel egress", UsedFor: []string{fleet.UseExternalEgress},
		Status: fleet.SourceStale, Scope: fleet.ScopeCluster}
	worst, why := -1, ""
	var latest time.Time
	for _, p := range views {
		c := p.Status.Contract
		if c == nil || c.ServiceName == "" {
			continue
		}
		reconciled := parseK8sTime(p.Status.LastReconciledAt)
		st, ok := delivery.EgressCoverage(p, reconciled, now)
		if !ok {
			continue
		}
		row.Services = append(row.Services, fleet.NewServiceKeyDomain(contract.OCIDomain(c.ResolvedRef), c.ServiceName))
		at := time.Time{}
		if reconciled != nil {
			at = *reconciled
		}
		if len(row.Services) == 1 || at.After(latest) {
			latest, row.Evidence = at, fmt.Sprintf("node agent on %d/%d nodes", st.NodesWithAgent, st.Nodes)
		}
		if why == "" {
			why = st.Why // every coverage is stale until a fresh one says otherwise
		}
		if status, rank := egressRowStatus(st.State); st.Reason != "stale-status" && rank > worst {
			worst, row.Status, why = rank, status, st.Why
		}
	}
	if row.Services == nil {
		return fleet.Provider{}, false
	}
	if row.Status != fleet.SourceAvailable {
		row.Limitations = []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Source: source,
			Message: "the node agent cannot rule out an external call: " + why}}
	}
	return row, true
}
