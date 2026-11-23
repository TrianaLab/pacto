package detect

import (
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ManagedUpgradeHint is the hint of a managed-resources row that names a
// kind the operator may not list: the chart grants the read of every
// catalog group the cluster serves when helm runs.
const ManagedUpgradeHint = "helm upgrade <release> <chart> --reuse-values"

// ClaimUnverified starts the managed-resources detail that counts the
// resources whose claim labels no composite confirms.
const ClaimUnverified = "MANAGED_CLAIM_UNVERIFIED"

// ManagedEntry is the managed-resources entry of a run that listed the
// catalog kinds of groups and found total resources, unverified of which
// name a claim no composite confirms; unread are the <group>/<plural> kinds
// the operator may not list. A namespace-list install reads none and is
// unknown; an unread kind makes the entry partial with the helm upgrade
// hint. The details stay within [MaxDetails]: the reason counts every
// unread kind.
func ManagedEntry(groups, unread []string, total, unverified int, scoped bool) Entry {
	const kind = fleet.ProviderManagedResources
	if scoped {
		// ponytail: managed resources are mostly cluster-scoped; read the
		// namespaced kinds in the watched namespaces if a scoped install asks.
		return UnknownEntry(kind, "a namespace-scoped install does not read managed resources")
	}
	e := Entry{Kind: kind, Source: "API discovery", Completeness: Complete, Scope: fleet.ScopeCluster,
		Value: "none found", Evidence: "the cluster serves no managed-resource group the catalog names"}
	if len(groups) > 0 {
		e.Value, e.Evidence = strconv.Itoa(total)+" managed resources", "lists the catalog kinds of "+strings.Join(groups, ", ")
	}
	if unverified > 0 {
		e.Details = []string{ClaimUnverified + ": " + strconv.Itoa(unverified) + " resources name a claim no composite confirms; their claim labels are ignored"}
	}
	if len(unread) > 0 {
		e.Completeness, e.Hint = Partial, ManagedUpgradeHint
		e.Reason = strconv.Itoa(len(unread)) + " managed-resource kinds served since the chart was installed are not granted"
		for _, k := range unread[:min(len(unread), MaxDetails-len(e.Details))] {
			e.Details = append(e.Details, "unread "+k)
		}
	}
	return e
}
