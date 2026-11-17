package fleet

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/trianalab/pacto/v4/pkg/validation"
)

// inferredInput is one workload a source reported, with the cluster its
// collection resolved to ("" when unknown).
type inferredInput struct {
	source  string
	cluster string
	raw     RawInferredService
}

// workloadKey identifies one workload: cluster (only in a snapshot with
// several clusters), namespace, kind and name.
type workloadKey struct{ cluster, namespace, kind, name string }

// boundInferred is the inferred services of col that [Build] reads: at most
// [MaxInferredServices], the first in (namespace, kind, name) order, with the
// INFERRED_SERVICES_CAPPED limitation when some were left out.
func boundInferred(col *Collection, source string) ([]RawInferredService, []Limitation) {
	in := col.InferredServices
	if len(in) <= MaxInferredServices {
		return in, nil
	}
	in = slices.Clone(in)
	slices.SortFunc(in, func(a, b RawInferredService) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return in[:MaxInferredServices], []Limitation{{
		Code: LimitationInferredServicesCapped, Source: source,
		Message: strconv.Itoa(len(in)-MaxInferredServices) + " workloads were not read past the bound of " + strconv.Itoa(MaxInferredServices) +
			"; raise MaxInferredServices if a real cluster hits it",
	}}
}

// addInferredServices makes each input an inferred service, unless a Pacto
// target runs as its workload. Two sources reporting one workload merge into
// one record. It runs after aggregateServices, so it never meets a service it
// would aggregate.
func addInferredServices(snap *FleetSnapshot, inputs []inferredInput) {
	declared := declaredWorkloads(snap)
	verified := declarationVerified(snap)
	for _, in := range inputs {
		key := in.key(snap.clusterKeyed)
		if _, ok := declared[key]; ok {
			continue
		}
		rec, lims := inferredRecord(in, snap.clusterKeyed)
		snap.Limitations = append(snap.Limitations, lims...)
		if rec == nil {
			continue
		}
		if existing := snap.Services[rec.Key]; existing != nil {
			mergeInferred(snap, existing, rec, in.source)
			continue
		}
		snap.Services[rec.Key] = rec
		if !verified(in.cluster, in.raw.Namespace) {
			snap.Limitations = append(snap.Limitations, Limitation{
				Code: LimitationDeclarationUnverified, Source: in.source, Message: unverifiedMessage(rec),
			})
		}
	}
}

func (in inferredInput) key(multi bool) workloadKey {
	k := workloadKey{namespace: in.raw.Namespace, kind: in.raw.Kind, name: in.raw.Name}
	if multi {
		k.cluster = in.cluster
	}
	return k
}

// mergeInferred folds a second source's record of one workload into existing,
// keeping the newest ObservedAt. A declared service with that name is never
// changed: the input is dropped with a limitation naming its source.
func mergeInferred(snap *FleetSnapshot, existing, rec *ServiceRecord, source string) {
	if !existing.Inferred() {
		snap.Limitations = append(snap.Limitations, Limitation{
			Code: LimitationSourceRecordInvalid, Source: source,
			Message: "source " + source + " reported a workload named like a declared service; it was left out",
		})
		return
	}
	existing.Sources = appendUnique(existing.Sources, source)
	if at := rec.Workload.ObservedAt; at != nil && (existing.Workload.ObservedAt == nil || at.After(*existing.Workload.ObservedAt)) {
		existing.Workload.ObservedAt = at
	}
}

// declaredWorkloads is the workloads Pacto targets run as, keyed like an
// inferred input, with the target that runs as each.
func declaredWorkloads(snap *FleetSnapshot) map[workloadKey]TargetKey {
	out := map[workloadKey]TargetKey{}
	for k, t := range snap.Targets {
		if t.workload == nil {
			continue
		}
		wk := workloadKey{namespace: t.Scope, kind: t.workload.Kind, name: t.workload.Name}
		if snap.clusterKeyed {
			wk.cluster = t.Cluster
		}
		out[wk] = k
	}
	return out
}

// declarationVerified reports whether a Kubernetes source read the Pacto
// targets of a (cluster, namespace) completely: it read every namespace and is
// available, it read that namespace and names no failure there, or it found
// no Pacto CRD, so no Pacto target exists there.
func declarationVerified(snap *FleetSnapshot) func(cluster, namespace string) bool {
	clusterOf := map[string]string{}
	for _, c := range snap.Clusters {
		for _, src := range c.Sources {
			clusterOf[src] = c.ID
		}
	}
	unreadable, crdAbsent := map[[2]string]bool{}, map[string]bool{}
	for _, l := range snap.Limitations {
		if ns, ok := unreadableNamespace(l); ok {
			unreadable[[2]string{l.Source, ns}] = true
		}
		crdAbsent[l.Source] = crdAbsent[l.Source] || l.Code == LimitationPactoCRDNotInstalled
	}
	read := func(st SourceState, namespace string) bool {
		everyNamespace := len(st.Namespaces) == 0 && st.NamespacesTotal == 0
		return crdAbsent[st.ID] || st.Status == SourceAvailable && everyNamespace ||
			slices.Contains(st.Namespaces, namespace) && !unreadable[[2]string{st.ID, namespace}]
	}
	return func(cluster, namespace string) bool {
		return slices.ContainsFunc(snap.Sources, func(st SourceState) bool {
			return st.Kind == SourceKindKubernetes && (!snap.clusterKeyed || clusterOf[st.ID] == cluster) && read(st, namespace)
		})
	}
}

// inferredRecord is the inferred service of one input, or nil with a
// limitation when its identity is invalid. It is the one gate every source
// passes: an image, port or probe holding a control character or past its
// bound is dropped with a limitation naming the field, never the value, and
// a probe path loses its query and fragment.
func inferredRecord(in inferredInput, multi bool) (*ServiceRecord, []Limitation) {
	r := in.raw
	if !validWorkloadPart(r.Namespace) || !validWorkloadPart(r.Kind) || !validWorkloadPart(r.Name) {
		return nil, []Limitation{{
			Code: LimitationSourceRecordInvalid, Source: in.source,
			Message: "source " + in.source + " reported a workload with an invalid identity; it was left out",
		}}
	}
	cluster := ""
	if multi {
		cluster = in.cluster
	}
	name := K8sCaller(cluster, r.Namespace, r.Kind, r.Name)
	w := &InferredWorkload{Source: in.source, Cluster: in.cluster, Namespace: r.Namespace, Kind: r.Kind, Name: r.Name, ObservedAt: copyTime(r.ObservedAt)}
	var lims []Limitation
	for _, field := range readShape(w, r) {
		lims = append(lims, Limitation{
			Code: LimitationSourceRecordInvalid, Source: in.source,
			Message: "workload " + name + " " + field + " holds a control character or is too long; it was left out",
		})
	}
	return &ServiceRecord{
		Key: NewServiceKey(name), Name: name, Declaration: DeclarationInferred, Workload: w,
		Sources: []string{in.source}, BusinessDomainUnknown: true, SummaryUnknown: true,
	}, lims
}

// readShape copies the image, ports and probes of r that pass cleanText into
// w and returns the field paths of those it dropped.
func readShape(w *InferredWorkload, r RawInferredService) (dropped []string) {
	if r.Image != nil {
		if cleanText(r.Image.Value, MaxInferredImageRunes) && cleanText(r.Image.From, MaxInferredPathRunes) {
			w.Image = &InferredValue{Value: r.Image.Value, From: r.Image.From}
		} else {
			dropped = append(dropped, "image")
		}
	}
	for i, p := range r.Ports {
		if cleanText(p.Name, MaxInferredPathRunes) && cleanText(p.Protocol, MaxInferredPathRunes) && cleanText(p.From, MaxInferredPathRunes) {
			w.Ports = append(w.Ports, p)
		} else {
			dropped = append(dropped, "ports["+strconv.Itoa(i)+"]")
		}
	}
	for i, p := range r.Probes {
		p.Path, _, _ = strings.Cut(p.Path, "?")
		p.Path, _, _ = strings.Cut(p.Path, "#")
		if cleanText(p.Kind, MaxInferredPathRunes) && cleanText(p.Path, MaxInferredPathRunes) && cleanText(p.Port, MaxInferredPathRunes) && cleanText(p.From, MaxInferredPathRunes) {
			w.Probes = append(w.Probes, p)
		} else {
			dropped = append(dropped, "probes["+strconv.Itoa(i)+"]")
		}
	}
	return dropped
}

// cleanText reports whether an untrusted value may be stored: at most limit
// runes and no control character.
func cleanText(v string, limit int) bool {
	return utf8.RuneCountInString(v) <= limit && !strings.ContainsFunc(v, unicode.IsControl)
}

// validWorkloadPart reports whether v may be one part of a [K8sCaller] name.
func validWorkloadPart(v string) bool {
	return v != "" && !strings.Contains(v, "/") && cleanText(v, 253)
}

// unverifiedMessage is the DECLARATION_UNVERIFIED message of an inferred
// record; inferredLimitations finds the record's own limitation by it.
func unverifiedMessage(s *ServiceRecord) string {
	return "Pacto could not read Pacto targets in " + s.Workload.Namespace + "; a contract may describe " + s.Name
}

// inferredLimitations is the limitations Build recorded about one inferred
// record: its DECLARATION_UNVERIFIED and the fields it dropped.
func inferredLimitations(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	var out []Limitation
	for _, l := range snap.Limitations {
		if !slices.Contains(s.Sources, l.Source) {
			continue
		}
		if l.Code == LimitationDeclarationUnverified && l.Message == unverifiedMessage(s) ||
			l.Code == LimitationSourceRecordInvalid && strings.HasPrefix(l.Message, "workload "+s.Name+" ") {
			out = append(out, l)
		}
	}
	return out
}

// inferredUnverified reports whether Build marked an inferred record
// DECLARATION_UNVERIFIED.
func inferredUnverified(snap *FleetSnapshot) func(*ServiceRecord) bool {
	marked := map[string]bool{}
	for _, l := range snap.Limitations {
		if l.Code == LimitationDeclarationUnverified {
			marked[l.Message] = true
		}
	}
	return func(s *ServiceRecord) bool { return marked[unverifiedMessage(s)] }
}

// observingSourcesOf is the ids of the sources that reported an observed edge.
func observingSourcesOf(observed []observedInput) map[string]bool {
	out := map[string]bool{}
	for _, oi := range observed {
		out[oi.source] = true
	}
	return out
}

// callsObserved reports whether an available source that reports observed
// calls covers the cluster of an inferred workload: only then does an empty
// calls list mean none were seen.
func (q *Query) callsObserved(w *InferredWorkload) bool {
	covers := func(string) bool { return true }
	if len(q.snap.Clusters) > 1 {
		i := slices.IndexFunc(q.snap.Clusters, func(c ClusterRecord) bool { return c.ID == w.Cluster })
		covers = func(id string) bool { return i >= 0 && slices.Contains(q.snap.Clusters[i].Sources, id) }
	}
	return slices.ContainsFunc(q.snap.Sources, func(st SourceState) bool {
		return q.snap.observingSources[st.ID] && st.Status == SourceAvailable && covers(st.ID)
	})
}

// inferredServiceDetail is the detail of an inferred service: what Pacto read
// from its workload, the declared services it was seen calling and where it
// read them. It has no revision, ownership, placement or neighborhood, so
// every such preview is empty.
func (q *Query) inferredServiceDetail(s *ServiceRecord) *EntityDetail {
	self := serviceEntityRef(s)
	var calls []EntityRef
	for _, c := range q.snap.UncontractedCallers {
		if c.Entity == s.Key && q.snap.Services[c.ToService] != nil {
			calls = append(calls, q.serviceRef(c.ToService))
		}
	}
	var lims []AttributedLimitation
	for _, l := range inferredLimitations(q.snap, s) {
		lims = append(lims, AttributedLimitation{Limitation: l})
	}
	observed := q.callsObserved(s.Workload)
	if !observed {
		lims = append(lims, AttributedLimitation{Limitation: Limitation{
			Code: LimitationEvidenceMissing, Source: s.Workload.Source,
			Message: "no traffic source is read, so the calls of " + s.Name + " are unknown",
		}})
	}
	var sources []EntityRef
	for _, st := range q.snap.Sources {
		if slices.Contains(s.Sources, st.ID) {
			sources = append(sources, sourceEntityRef(st))
		}
	}
	_, cfgs, pols := q.currentRevisionContent(nil)
	data := &ServiceDetailData{
		Domain: s.Domain, Capabilities: stringsPreview(nil), Sources: refPreview(sources),
		UnresolvedDependencies: unresolvedDependencyItems(self, nil), ConsumedOfferings: offeringUsesPreview(nil),
		InferredOfferings: []InferredOffering{}, Policies: pols, Configurations: cfgs,
		Revisions: refPreview(nil), ActiveRevisions: refPreview(nil), Signatures: revisionSignatures(nil),
		Deployments: refPreview(nil), Dependencies: refPreview(nil), Dependents: refPreview(nil), ReferencedBy: refPreview(nil),
		Placement: PlacementPreview{Items: []EnvironmentPlacement{}, Differences: []validation.EnvironmentDifference{}},
		Changes:   q.serviceChanges(s), Relationships: relationshipsPreview(nil),
		Findings: attributedTargetFindingsPreview(nil), Evidence: evidencePreview(nil),
		Limitations: attributedLimitationsPreview(lims), Workload: jsonClone(s.Workload),
		Calls: refPreview(calls), CallsObserved: observed,
	}
	return &EntityDetail{Meta: q.productMeta(), Entity: self, Service: data, Actions: []string{}}
}
