package detect

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ManagedUpgradeHint is the hint of a managed-resources row that names a
// kind the operator may not list: the chart grants the read of every
// catalog group the cluster serves when helm runs.
const ManagedUpgradeHint = "helm upgrade <release> <chart> --reuse-values"

// ClaimUnverified starts the managed-resources detail that counts the
// resources whose claim labels no composite Pacto read confirms.
const ClaimUnverified = "MANAGED_CLAIM_UNVERIFIED"

// ManagedEntry is the managed-resources entry of a run that listed the
// catalog kinds of groups and found total resources, unverified of which
// name a claim no composite Pacto read confirms; unread are the <group>/<plural> kinds
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
		e.Details = []string{ClaimUnverified + ": " + strconv.Itoa(unverified) + " resources name a claim Pacto could not confirm with a composite it read; their claim labels are ignored"}
	}
	if len(unread) > 0 {
		e.Completeness, e.Hint = Partial, ManagedUpgradeHint
		e.Reason = strconv.Itoa(len(unread)) + " managed-resource kinds the operator may not list"
		for _, k := range unread[:min(len(unread), MaxDetails-len(e.Details))] {
			e.Details = append(e.Details, "unread "+k)
		}
	}
	return e
}

// TerraformEntry is the terraform entry of a run that read the listed
// states read and could not read the states failed (location to reason):
// complete when every listed state was read, partial when some failed and
// unknown when none was read. A failure is one detail per location.
func TerraformEntry(read []string, failed map[string]string) Entry {
	e := Entry{Kind: "terraform", Source: "listed Terraform states", Completeness: Complete, Scope: fleet.ScopeCluster,
		Value: strconv.Itoa(len(read)) + " listed states read"}
	if len(read) > 0 {
		e.Evidence = "reads " + strings.Join(read, ", ")
	}
	if len(failed) == 0 {
		return e
	}
	e.Completeness, e.Reason = Partial, strconv.Itoa(len(failed))+" of "+strconv.Itoa(len(read)+len(failed))+" listed states could not be read"
	if len(read) == 0 {
		e.Completeness, e.Value = Unknown, ""
	}
	for _, loc := range slices.Sorted(maps.Keys(failed))[:min(len(failed), MaxDetails)] {
		e.Details = append(e.Details, loc+": "+failed[loc])
	}
	return e
}

// CloudInventoryEntry is the cloud-inventory entry. cloud is the cloud
// controller.cloudInventory names ("off" or empty when none); identity the
// read-only workload identity bound to the operator ServiceAccount, ok when
// it holds, and reason why it does not; err the inventory read's failure.
// Off with no identity writes no entry (Kind empty); off with one names the
// opt-in; on with none names the missing identity. Nothing off reads as
// complete.
func CloudInventoryEntry(cloud, identity, reason string, ok bool, err error) Entry {
	const kind = "cloud-inventory"
	off := cloud == "" || cloud == "off"
	switch {
	case off && !ok:
		return Entry{}
	case off:
		return Entry{Kind: kind, Value: "off", Source: "controller.cloudInventory", Completeness: Unknown, Scope: fleet.ScopeCluster,
			Reason: "set controller.cloudInventory to read the inventory with " + identity,
			Hint:   "helm upgrade <release> <chart> --reuse-values --set controller.cloudInventory=<cloud>"}
	case !ok:
		return Entry{Kind: kind, Value: "off", Source: "controller.cloudInventory=" + cloud, Completeness: Unknown, Scope: fleet.ScopeCluster,
			Reason: reason, Hint: "bind a read-only " + cloud + " identity to the operator ServiceAccount (serviceAccount.annotations)"}
	case err != nil:
		return Entry{Kind: kind, Source: "controller.cloudInventory=" + cloud, Completeness: Unknown, Scope: fleet.ScopeCluster,
			Reason: "reading the " + cloud + " inventory failed: " + err.Error()}
	}
	return Entry{Kind: kind, Value: cloud, Evidence: "read with " + identity, Source: "controller.cloudInventory=" + cloud,
		Completeness: Complete, Scope: fleet.ScopeCluster}
}
