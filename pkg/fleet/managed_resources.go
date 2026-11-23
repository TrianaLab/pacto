package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// kindConnectionSecret is the [InferredSource] kind of a use joined through
// a managed resource's connection Secret.
const kindConnectionSecret = "connectionSecret"

// sourcedResource pairs a managed resource with the id of the source that read it.
type sourcedResource struct {
	res    RawManagedResource
	source string
}

// sourcedSecretRef pairs a Secret ref with the id of the source that read it.
type sourcedSecretRef struct {
	ref    RawSecretRef
	source string
}

// externalInputs is what every collection reports about external systems,
// each value with the id of its source: the uses, the managed resources and
// the Secret refs.
type externalInputs struct {
	uses      []sourcedUse
	resources []sourcedResource
	refs      []sourcedSecretRef
}

// add appends col's external inputs, read by source.
func (in *externalInputs) add(source string, col *Collection) {
	for _, u := range col.ExternalUses {
		in.uses = append(in.uses, sourcedUse{use: u, source: source})
	}
	for _, m := range col.ManagedResources {
		in.resources = append(in.resources, sourcedResource{res: m, source: source})
	}
	for _, ref := range col.SecretRefs {
		in.refs = append(in.refs, sourcedSecretRef{ref: ref, source: source})
	}
}

// visibleResources is the resources visible says yes to, and how many it hides.
func visibleResources(rs []ManagedResource, visible func(cluster, namespace string) bool) (kept []ManagedResource, hidden int) {
	for _, r := range rs {
		if resourceVisible(r, visible) {
			kept = append(kept, r)
		} else {
			hidden++
		}
	}
	return kept, hidden
}

// secretKey is one Secret: cluster, namespace and name.
type secretKey struct{ cluster, namespace, name string }

// ingestManagedResources puts each managed resource on the record of its
// external system, created when absent so a resource nobody uses is still a
// node, and returns the uses its connection Secrets join (see
// managedResourceUses). A resource whose identity is not one is dropped and
// counted in one MANAGED_RESOURCE_INVALID per source. It runs once the
// targets and the inferred services exist.
func ingestManagedResources(snap *FleetSnapshot, cat *externalcatalog.Catalog, resources []sourcedResource, refs []sourcedSecretRef) []sourcedUse {
	invalid := map[string]int{}
	var valid []sourcedResource
	for _, sr := range resources {
		r := sr.res
		if _, err := externalid.Parse(r.Identity); err != nil {
			invalid[sr.source]++
			continue
		}
		rec := externalRecord(snap, cat, ExternalKey(r.Identity))
		rec.Resources = append(rec.Resources, ManagedResource{Source: r.Source, Type: r.Type, Namespace: r.Namespace, Name: r.Name,
			Cluster: r.Cluster, ClusterName: r.ClusterName, Location: r.Location, Claim: r.Claim, Engine: r.Engine,
			EngineRequested: r.EngineRequested, Missing: slices.Clone(r.Missing), Provenance: ProvenanceInferred, ObservedAt: copyTime(r.ObservedAt)})
		valid = append(valid, sr)
	}
	for _, src := range slices.Sorted(maps.Keys(invalid)) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationManagedResourceInvalid, Source: src,
			Message: fmt.Sprintf("%d managed resources source %s reported carry no valid external identity; they were dropped", invalid[src], src)})
	}
	return managedResourceUses(snap, valid, refs)
}

// managedResourceUses joins each connection Secret of resources to the
// workloads whose pod template references a Secret of that cluster,
// namespace and name, and returns one inferred use per join, attributed to
// the source that reported the ref so its target is on the ref's cluster.
// A workload no service runs as gives a use with no service, which
// foldExternalUses counts and never guesses.
func managedResourceUses(snap *FleetSnapshot, resources []sourcedResource, refs []sourcedSecretRef) []sourcedUse {
	bySecret := map[secretKey][]sourcedSecretRef{}
	seen := map[RawSecretRef]bool{}
	for _, sr := range refs {
		if seen[sr.ref] {
			continue
		}
		seen[sr.ref] = true
		k := secretKey{sr.ref.Cluster, sr.ref.Namespace, sr.ref.Secret}
		bySecret[k] = append(bySecret[k], sr)
	}
	services := workloadServices(snap)
	var uses []sourcedUse
	for _, sr := range resources {
		r := sr.res
		for _, cs := range r.ConnectionSecrets {
			ns, name, ok := strings.Cut(cs, "/")
			if !ok {
				continue
			}
			for _, ref := range bySecret[secretKey{r.Cluster, ns, name}] {
				uses = append(uses, connectionSecretUse(r, ref, services))
			}
		}
	}
	return uses
}

// connectionSecretUse is the inferred use ref's workload makes of r.
func connectionSecretUse(r RawManagedResource, sr sourcedSecretRef, services map[workloadKey]ServiceKey) sourcedUse {
	ref := sr.ref
	return sourcedUse{source: sr.source, svc: services[workloadKey{ref.Cluster, ref.Namespace, ref.Kind, ref.Name}], use: RawExternalUse{
		Namespace: ref.Namespace, Identity: r.Identity, Class: externalcatalog.ClassManagedCloud,
		Confidence: externalcatalog.ConfidenceLikely, Kind: kindConnectionSecret,
		Where: strings.ToLower(ref.Kind) + "/" + ref.Name + " secret/" + ref.Secret,
		Entry: r.Entry, Catalog: r.Catalog, ObservedAt: copyTime(r.ObservedAt),
	}}
}

// workloadServices maps each workload to the service that runs it: the
// service of a Kubernetes target that names it as its workload (the lowest
// target key when several do), else the inferred service read from it.
func workloadServices(snap *FleetSnapshot) map[workloadKey]ServiceKey {
	out := map[workloadKey]ServiceKey{}
	for _, k := range slices.Sorted(maps.Keys(snap.Targets)) {
		t := snap.Targets[k]
		if w := t.workload; w != nil && kubeTarget(t) {
			wk := workloadKey{t.Cluster, t.Scope, w.Kind, w.Name}
			out[wk] = cmp.Or(out[wk], t.ServiceKey)
		}
	}
	for k, s := range snap.Services {
		if w := s.Workload; s.inferredWorkload() {
			wk := workloadKey{w.Cluster, w.Namespace, w.Kind, w.Name}
			out[wk] = cmp.Or(out[wk], k)
		}
	}
	return out
}

// resourceVisible reports whether a viewer visible says yes to may read r:
// one with a cluster in its namespace there (the cluster scope for a
// cluster-scoped object, a state or an inventory item), one with no cluster
// in the cluster scope.
func resourceVisible(r ManagedResource, visible func(cluster, namespace string) bool) bool {
	if r.Cluster == "" {
		return visible("", "")
	}
	return visible(r.Cluster, r.Namespace)
}

// hidesResources reports whether visible hides any managed resource of s.
func hidesResources(s *FleetSnapshot, visible func(cluster, namespace string) bool) bool {
	for _, rec := range s.Externals {
		for _, r := range rec.Resources {
			if !resourceVisible(r, visible) {
				return true
			}
		}
	}
	return false
}

// sortResources orders resources by source, cluster, namespace and name.
func sortResources(rs []ManagedResource) {
	slices.SortFunc(rs, func(a, b ManagedResource) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Cluster, b.Cluster), cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.Type, b.Type), cmp.Compare(a.Location, b.Location))
	})
}
