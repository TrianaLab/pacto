package fleet

import (
	"maps"
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
// contract state, by record key (see resourceContract). An object reference
// two resources of different API groups share on one cluster is ambiguous
// (the reference names a bare Kind), so it proves neither. Every read of one
// complete identity is the same resource: one read through an offering puts
// them all through it.
func (q *Query) resourceContracts() map[ExternalKey][]ManagedResource {
	proofs := q.offeringProofs()
	recs := slices.Collect(maps.Values(q.snap.Externals))
	// ponytail: other-egress classifies under its own key, so a folded
	// resource matches by object or claim reference only; carry each
	// resource's identity if real fleets fold managed resources.
	if o := q.externals[OtherEgress]; o != nil {
		recs = append(recs, o)
	}
	ambiguous := ambiguousObjectRefs(recs)
	out := map[ExternalKey][]ManagedResource{}
	for _, rec := range recs {
		if len(rec.Resources) == 0 {
			continue
		}
		rs := slices.Clone(rec.Resources)
		for i := range rs {
			rs[i].Contract, rs[i].Offering, rs[i].Evidence = resourceContract(rec, rs[i], proofs, ambiguous)
		}
		sameIdentity(rec, rs)
		out[rec.Key] = rs
	}
	return out
}

// sameIdentity puts every complete read of rec's complete identity through
// the offering one of them is through: a cluster object, a Terraform state
// entry and an inventory item of one database are one resource.
func sameIdentity(rec *ExternalRecord, rs []ManagedResource) {
	if !completeIdentity(rec.Key) {
		return
	}
	i := slices.IndexFunc(rs, func(r ManagedResource) bool { return len(r.Missing) == 0 && r.Contract == ContractOffering })
	if i < 0 {
		return
	}
	for j := range rs {
		if len(rs[j].Missing) == 0 && rs[j].Contract != ContractOffering {
			rs[j].Contract, rs[j].Offering, rs[j].Evidence = rs[i].Contract, rs[i].Offering, rs[i].Evidence
		}
	}
}

// completeIdentity reports whether key is a managed identity every part of
// which was read.
func completeIdentity(key ExternalKey) bool {
	return len(strings.Split(string(key), "/")) == completeIdentitySegments
}

// objectRef is r's object reference, "" when r is no cluster object.
func objectRef(r ManagedResource) string {
	if r.Cluster == "" || !slices.Contains(clusterObjectSources, r.Source) {
		return ""
	}
	_, kind, _ := strings.Cut(r.Type, "/")
	return ManagedObjectRef(kind, r.Namespace, r.Name)
}

// ambiguousObjectRefs is the cluster and object reference pairs resources
// of more than one type share, keyed cluster, NUL, reference.
func ambiguousObjectRefs(recs []*ExternalRecord) map[string]bool {
	types := map[string]string{}
	out := map[string]bool{}
	for _, rec := range recs {
		for _, r := range rec.Resources {
			ref := objectRef(r)
			if ref == "" {
				continue
			}
			k := r.Cluster + "\x00" + ref
			if t, ok := types[k]; ok && t != r.Type {
				out[k] = true
			}
			types[k] = r.Type
		}
	}
	return out
}

// carryContracts keeps, on a restricted view's resources (own), the contract
// state the unrestricted query gave the same resource (full): a proof the
// view hides never reads outside, and then names no offering or evidence.
// A resource full does not hold keeps its own state.
func carryContracts(own, full map[ExternalKey][]ManagedResource) map[ExternalKey][]ManagedResource {
	for key, rs := range own {
		for i := range rs {
			j := slices.IndexFunc(full[key], func(f ManagedResource) bool { return sameRead(f, rs[i]) })
			if j < 0 || full[key][j].Contract == rs[i].Contract {
				continue
			}
			rs[i].Contract, rs[i].Offering, rs[i].Evidence = full[key][j].Contract, nil, ""
		}
	}
	return own
}

// sameRead reports whether a and b are the same read of a managed resource.
func sameRead(a, b ManagedResource) bool {
	return a.Source == b.Source && a.Type == b.Type && a.Cluster == b.Cluster && a.Namespace == b.Namespace && a.Name == b.Name && a.Location == b.Location
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
// otherwise; an ambiguous object reference proves nothing. A partial
// identity never matches, so one record cannot put
// every partially read resource through an offering.
func resourceContract(rec *ExternalRecord, r ManagedResource, proofs map[string][]offeringProof, ambiguous map[string]bool) (string, *EntityRef, string) {
	var onCluster []string
	if ref := objectRef(r); ref != "" && !ambiguous[r.Cluster+"\x00"+ref] {
		onCluster = append(onCluster, ref)
	}
	if r.Cluster != "" && r.Claim != "" {
		onCluster = append(onCluster, r.Claim)
	}
	for _, ref := range onCluster {
		for _, p := range proofs[ref] {
			if p.clusters[r.Cluster] {
				return ContractOffering, &p.offering, p.evidence
			}
		}
	}
	if len(r.Missing) == 0 && completeIdentity(rec.Key) {
		if ps := proofs[string(rec.Key)]; len(ps) > 0 {
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
		// A contributed provider's offering is inferred, not accepted: only an
		// accepted (declared) offering puts a resource through it.
		if prov == nil || prov.Offering == "" || prov.Contribution != nil || prov.Name != rec.Provider || !q.currentObserved(rec) {
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
