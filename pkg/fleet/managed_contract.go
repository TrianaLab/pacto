package fleet

import (
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// Contract states of a [ManagedResource].
const (
	ContractOffering = "offering"
	ContractOutside  = "outside"
)

// clusterObjectSources are the managed-resource sources that read a cluster
// object, the only ones an object reference names.
var clusterObjectSources = []string{"crossplane", "ack", "config-connector", "aso"}

// completeIdentitySegments is how many segments a managed identity has when
// every part externalid.Managed needs was read.
const completeIdentitySegments = 5

// ManagedObjectRef is how a provisioning record names a cluster object it
// provisioned: "<Kind> <namespace>/<name>", or "<Kind> <name>" for a
// cluster-scoped one.
func ManagedObjectRef(kind, namespace, name string) string {
	if namespace == "" {
		return kind + " " + name
	}
	return kind + " " + namespace + "/" + name
}

// offeringProof is a provisioning record that may put a managed resource
// through an offering: verified, current, Observed and from the offering
// provider the consumer's use resolves to. clusters are the clusters of the
// consumer's targets in the record's environment.
type offeringProof struct {
	offering EntityRef
	evidence string
	clusters map[string]bool
}

// resourceContracts is every managed resource the answers name with its
// contract state, by record key (see resourceContract).
func (q *Query) resourceContracts() map[ExternalKey][]ManagedResource {
	proofs := q.offeringProofs()
	out := map[ExternalKey][]ManagedResource{}
	classify := func(rec *ExternalRecord) {
		if len(rec.Resources) == 0 {
			return
		}
		rs := slices.Clone(rec.Resources)
		for i := range rs {
			rs[i].Contract, rs[i].Offering, rs[i].Evidence = resourceContract(rec, rs[i], proofs)
		}
		out[rec.Key] = rs
	}
	for _, rec := range q.snap.Externals {
		classify(rec)
	}
	// ponytail: other-egress classifies under its own key, so a folded
	// resource matches by object or claim reference only; carry each
	// resource's identity if real fleets fold managed resources.
	if o := q.externals[OtherEgress]; o != nil {
		classify(o)
	}
	return out
}

// managedResources is rec's managed resources with their contract state.
func (q *Query) managedResources(rec *ExternalRecord) []ManagedResource {
	return q.contracts()[rec.Key]
}

// outsideContract counts rec's managed resources no offering produced.
func (q *Query) outsideContract(rec *ExternalRecord) int {
	n := 0
	for _, r := range q.managedResources(rec) {
		n += b2i(r.Contract == ContractOutside)
	}
	return n
}

// resourceContract is r's contract state on rec: offering, with the offering
// and the record's producer and time, when a proof names r by its object
// reference or its verified claim from a consumer target on r's cluster, or
// by rec's identity when it is complete and nothing was missing; outside
// otherwise. A partial identity never matches, so one record cannot put
// every partially read resource through an offering.
func resourceContract(rec *ExternalRecord, r ManagedResource, proofs map[string][]offeringProof) (string, *EntityRef, string) {
	var onCluster []string
	if r.Cluster != "" {
		if slices.Contains(clusterObjectSources, r.Source) {
			_, kind, _ := strings.Cut(r.Type, "/")
			onCluster = append(onCluster, ManagedObjectRef(kind, r.Namespace, r.Name))
		}
		if r.Claim != "" {
			onCluster = append(onCluster, r.Claim)
		}
	}
	for _, ref := range onCluster {
		for _, p := range proofs[ref] {
			if p.clusters[r.Cluster] {
				return ContractOffering, &p.offering, p.evidence
			}
		}
	}
	if id := string(rec.Key); len(r.Missing) == 0 && len(strings.Split(id, "/")) == completeIdentitySegments {
		if ps := proofs[id]; len(ps) > 0 {
			return ContractOffering, &ps[0].offering, ps[0].evidence
		}
	}
	return ContractOutside, nil, ""
}

// offeringProofs indexes by external reference the provisioning records
// that may put a resource through an offering (see offeringProof).
func (q *Query) offeringProofs() map[string][]offeringProof {
	providers := map[provisioningKey]ServiceKey{}
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if r := q.snap.Revisions[rel.FromRevision]; r != nil && provisioningUse(rel) {
			providers[provisioningKey{rel.FromService, r.Digest, provisioningKind(rel.Type), rel.To}] = rel.ToService
		}
	}
	out := map[string][]offeringProof{}
	for i := range q.snap.Provisioning {
		rec := &q.snap.Provisioning[i]
		prov := q.snap.Services[providers[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}]]
		if prov == nil || prov.Offering == "" || prov.Name != rec.Provider || !q.currentObserved(rec) {
			continue
		}
		out[rec.ExternalRef] = append(out[rec.ExternalRef], offeringProof{
			offering: offeringEntityRef(prov.Offering, prov.OfferingProvenance),
			evidence: "reported by " + rec.Producer + " at " + rec.ObservedAt.UTC().Format(time.RFC3339),
			clusters: q.envClusters(rec.ServiceKey, rec.Environment),
		})
	}
	return out
}

// currentObserved reports whether rec is a verified, fresh Observed report
// about its consumer's current revision.
func (q *Query) currentObserved(rec *ProvisioningRecord) bool {
	if !rec.Verified || rec.Outcome != string(evidence.Observed) || q.staleReport(rec) {
		return false
	}
	cur := q.currentRevision(q.snap.Services[rec.ServiceKey])
	return cur != nil && cur.Digest == rec.Digest
}

// envClusters is the clusters svc's targets in env run in.
func (q *Query) envClusters(svc ServiceKey, env string) map[string]bool {
	out := map[string]bool{}
	for _, tk := range q.snap.Services[svc].Targets {
		if t := q.snap.Targets[tk]; t != nil && t.Environment == env && t.Cluster != "" {
			out[t.Cluster] = true
		}
	}
	return out
}
