package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/trianalab/pacto/v4/pkg/validation"
	"github.com/trianalab/pacto/v4/pkg/workloadshape"
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

// scope is the namespace, kind and cluster of k.
func (k workloadKey) scope() WorkloadScope { return WorkloadScope{k.namespace, k.kind, k.cluster} }

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

// addInferredServices makes each input an inferred service (replaceInferred
// already removed those a Pacto target runs as). Two sources reporting one
// workload merge into one record. It runs after aggregateServices, so it never
// meets a service it would aggregate.
func addInferredServices(snap *FleetSnapshot, inputs []inferredInput) {
	verified := declarationVerified(snap)
	for _, in := range inputs {
		rec, lims := inferredServiceRecord(in, snap.clusterKeyed)
		snap.Limitations = append(snap.Limitations, lims...)
		if rec == nil {
			continue
		}
		if existing := snap.Services[rec.Key]; existing != nil {
			mergeInferred(snap, existing, rec, in.source)
			continue
		}
		rec.Workload.DeclarationUnverified = !verified(in.cluster, in.raw.Namespace)
		snap.Services[rec.Key] = rec
	}
	snap.Limitations = append(snap.Limitations, inferredSnapshotLimitations(snap)...)
	countInferred(snap)
}

// droppedFieldsPhrase marks the per-source count of workloads whose fields
// were left out, so a restricted view can rebuild it.
const droppedFieldsPhrase = " held a control character or a value past its bound in their image, ports, probes, group or repositories"

// inferredSnapshotLimitations is one DECLARATION_UNVERIFIED, one count of
// dropped fields and one count of ungrouped workloads per source, from the
// state each inferred record carries: one per record would fill every
// answer's bounded limitations.
func inferredSnapshotLimitations(snap *FleetSnapshot) []Limitation {
	unverified, namespaces, dropped, ambiguous := map[string]int{}, map[string]map[string]bool{}, map[string]int{}, map[string]int{}
	for _, s := range snap.Services {
		w := s.Workload
		if w == nil {
			continue
		}
		if w.DeclarationUnverified {
			unverified[w.Source]++
			if namespaces[w.Source] == nil {
				namespaces[w.Source] = map[string]bool{}
			}
			namespaces[w.Source][w.Cluster+"/"+w.Namespace] = true
		}
		dropped[w.Source] += b2i(len(w.Dropped) > 0)
		ambiguous[w.Source] += b2i(w.Group != nil && w.Group.Ambiguous != "")
	}
	var out []Limitation
	for _, src := range slices.Sorted(maps.Keys(unverified)) {
		out = append(out, Limitation{
			Code: LimitationDeclarationUnverified, Source: src,
			Message: "Pacto could not read the Pacto targets where inferred services from source " + src + " run (services: " +
				strconv.Itoa(unverified[src]) + ", namespaces: " + strconv.Itoa(len(namespaces[src])) + "), so a contract may describe them",
		})
	}
	for _, src := range slices.Sorted(maps.Keys(dropped)) {
		if dropped[src] > 0 {
			out = append(out, Limitation{
				Code: LimitationSourceRecordInvalid, Source: src,
				Message: "workloads from source " + src + " (" + strconv.Itoa(dropped[src]) + ")" + droppedFieldsPhrase + "; those fields were left out",
			})
		}
	}
	for _, src := range slices.Sorted(maps.Keys(ambiguous)) {
		if ambiguous[src] > 0 {
			out = append(out, Limitation{
				Code: LimitationServiceGroupAmbiguous, Source: src,
				Message: strconv.Itoa(ambiguous[src]) + " workloads from source " + src + " stay ungrouped because two groups of one rule claim them",
			})
		}
	}
	return out
}

// countInferred sets each source's inferred count to the inferred records
// that list it, so declared, invalid and merged workloads never count.
func countInferred(snap *FleetSnapshot) {
	for i := range snap.Sources {
		snap.Sources[i].InferredCount = 0
		for _, s := range snap.Services {
			if s.Inferred() && slices.Contains(s.Sources, snap.Sources[i].ID) {
				snap.Sources[i].InferredCount++
			}
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
	if !existing.inferredWorkload() {
		snap.Limitations = append(snap.Limitations, Limitation{
			Code: LimitationSourceRecordInvalid, Source: source,
			Message: "source " + source + " reported a workload named like a declared service; it was left out",
		})
		return
	}
	existing.Sources = appendUnique(existing.Sources, source)
	for _, f := range rec.Workload.Dropped {
		existing.Workload.Dropped = appendUnique(existing.Workload.Dropped, f)
	}
	if at := rec.Workload.ObservedAt; at != nil && (existing.Workload.ObservedAt == nil || at.After(*existing.Workload.ObservedAt)) {
		existing.Workload.ObservedAt = at
	}
}

// declarationVerified reports whether a Kubernetes source or a target snapshot
// read the Pacto targets of a (cluster, namespace) completely: it read every namespace and is
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
			return (st.Kind == SourceKindKubernetes || st.Kind == SourceKindTargetSnapshot) && (!snap.clusterKeyed || clusterOf[st.ID] == cluster) && read(st, namespace)
		})
	}
}

// inferredServiceRecord is the inferred service of one input, or nil with a
// limitation when its identity is invalid. It is the one gate every source
// passes: an image, port or probe holding a control character or past its
// bound is dropped and its field, never the value, recorded in Dropped, and
// a probe path loses its query and fragment.
func inferredServiceRecord(in inferredInput, multi bool) (*ServiceRecord, []Limitation) {
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
	w.Dropped = append(readShape(w, r), readGroup(w, r, cluster)...)
	return &ServiceRecord{
		Key: NewServiceKey(name), Name: name, Declaration: DeclarationInferred, Workload: w,
		Sources: []string{in.source}, BusinessDomainUnknown: true, SummaryUnknown: true,
	}, nil
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

// groupRuleWorkload is the fallback group: the workload alone.
const groupRuleWorkload = "workload"

// noGroupingFrom is where the fallback group was read from.
const noGroupingFrom = "no grouping label or annotation"

// droppedGroupFrom is where the fallback group was read from when the group a
// source sent was left out.
const droppedGroupFrom = "a grouping value Pacto read but left out (invalid)"

// groupRules are the rules a group may name, in precedence order. The first
// four are pkg/workloadmanager's manager names.
var groupRules = []string{"argocd", "flux-kustomization", "flux-helmrelease", "helm", "part-of", "instance", groupRuleWorkload}

// repositoryKinds are the GitOps kinds a repository may be read from.
var repositoryKinds = []string{"Application", "GitRepository", "Kustomization", "HelmRelease"}

// readGroup sets the group, repositories and repository reason of r that
// pass cleanText on w and returns the field paths of those it dropped. A
// group that fails, or none, becomes the workload's own group, rebuilt from
// its validated identity; cluster prefixes the key in a snapshot with several
// clusters.
func readGroup(w *InferredWorkload, r RawInferredService, cluster string) (dropped []string) {
	g := InferredGroup{Rule: groupRuleWorkload, Namespace: w.Namespace, Name: w.Kind + "/" + w.Name, From: noGroupingFrom}
	switch src := r.Group; {
	case src == nil:
	case src.Rule == groupRuleWorkload && cleanText(src.Ambiguous, 2*MaxInferredPathRunes):
		g.Ambiguous = src.Ambiguous
	case validGroup(*src):
		g = InferredGroup{Rule: src.Rule, Namespace: src.Namespace, Name: src.Name, From: src.From}
	default:
		g.From = droppedGroupFrom
		dropped = append(dropped, "group")
	}
	g.Key = strings.Join([]string{g.Rule, g.Namespace, g.Name}, "/")
	if cluster != "" {
		g.Key = cluster + "/" + g.Key
	}
	g.Label = groupLabel(g)
	w.Group = &g
	return append(dropped, readRepositories(w, r)...)
}

// validGroup reports whether a group other than the workload's own may be
// stored.
func validGroup(g InferredGroup) bool {
	return g.Rule != groupRuleWorkload && slices.Contains(groupRules, g.Rule) && g.Name != "" && g.Ambiguous == "" &&
		cleanText(g.Namespace, MaxInferredPathRunes) && cleanText(g.Name, MaxInferredPathRunes) && cleanText(g.From, MaxInferredPathRunes)
}

// readRepositories copies the first MaxInferredRepositories repositories of
// r that may be stored, and its repository reason, into w and returns the
// field paths of those it dropped.
func readRepositories(w *InferredWorkload, r RawInferredService) (dropped []string) {
	for i, repo := range r.Repositories {
		if i < MaxInferredRepositories && validRepository(repo) {
			w.Repositories = append(w.Repositories, repo)
		} else {
			dropped = append(dropped, "repositories["+strconv.Itoa(i)+"]")
		}
	}
	if cleanText(r.RepositoryUnknown, 2*MaxInferredPathRunes) {
		w.RepositoryUnknown = r.RepositoryUnknown
		for _, sc := range r.RepositoryUnknownFrom {
			// An object Pacto cannot check hides the reason from every viewer.
			if !cleanText(sc.Namespace, MaxInferredPathRunes) || !slices.Contains(repositoryKinds, sc.Kind) {
				sc = WorkloadScope{}
			}
			w.repositoryUnknownFrom = append(w.repositoryUnknownFrom, sc)
		}
	} else {
		dropped = append(dropped, "repositoryUnknown")
	}
	return dropped
}

// validRepository reports whether a repository may be stored.
func validRepository(r InferredRepository) bool {
	return r.URL != "" && cleanText(r.URL, MaxInferredImageRunes) && cleanText(r.Path, MaxInferredPathRunes) &&
		cleanText(r.From, MaxInferredPathRunes) && cleanText(r.Namespace, MaxInferredPathRunes) && slices.Contains(repositoryKinds, r.Kind)
}

// groupLabel is the one wording of a group, such as "Argo CD Application
// shop/cart".
func groupLabel(g InferredGroup) string {
	where := g.Name
	if g.Namespace != "" {
		where = g.Namespace + "/" + g.Name
	}
	switch g.Rule {
	case "argocd":
		return "Argo CD Application " + where
	case "flux-kustomization":
		return "Flux Kustomization " + where
	case "flux-helmrelease":
		return "Flux HelmRelease " + where
	case "helm":
		return "Helm release " + where
	case "part-of", "instance":
		return g.Rule + " " + g.Name + " in " + g.Namespace
	case groupRuleWorkload:
		if g.Ambiguous != "" {
			return "Ungrouped"
		}
	}
	return "This workload only"
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

// inferredLimitations is the limitations of one inferred record, built from
// the state it carries: its DECLARATION_UNVERIFIED and the fields it dropped.
func inferredLimitations(s *ServiceRecord) []Limitation {
	w := s.Workload
	var out []Limitation
	if w.DeclarationUnverified {
		out = append(out, Limitation{
			Code: LimitationDeclarationUnverified, Source: w.Source,
			Message: "Pacto could not read Pacto targets in " + w.Namespace + "; a contract may describe " + s.Name,
		})
	}
	for _, field := range w.Dropped {
		out = append(out, Limitation{
			Code: LimitationSourceRecordInvalid, Source: w.Source,
			Message: "workload " + s.Name + " " + field + " holds a control character or is too long; it was left out",
		})
	}
	if w.Group != nil && w.Group.Ambiguous != "" {
		out = append(out, Limitation{
			Code: LimitationServiceGroupAmbiguous, Source: w.Source,
			Message: s.Name + " stays ungrouped: " + w.Group.Ambiguous,
		})
	}
	return out
}

// observingSourcesOf is the ids of the sources that reported an observed edge
// from a workload caller (a [K8sCaller]): only such a source can attribute a
// call to an inferred service. A source naming callers by service.name, such
// as OTLP or Mira, never can.
func observingSourcesOf(observed []observedInput) map[string]bool {
	out := map[string]bool{}
	for _, oi := range observed {
		if _, _, _, _, ok := ParseK8sCaller(oi.edge.From); ok {
			out[oi.source] = true
		}
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
	for _, l := range inferredLimitations(s) {
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
	if s.HiddenDependencies > 0 {
		// A call to a service the viewer may not see was seen and hidden, so
		// an empty list must not read as none seen.
		observed = true
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
		HiddenDependencies: s.HiddenDependencies, HiddenDependents: s.HiddenDependents,
		NextAction: declareAction(s.Workload.Name, s.Workload.Namespace),
	}
	return &EntityDetail{Meta: q.productMeta(), Entity: self, Service: data, Actions: []string{}}
}

// Lines renders an inferred workload as text for the CLI and the TUI: where
// it runs, then its image, ports and probes, each with where Pacto read it.
func (w *InferredWorkload) Lines() []string {
	where := w.Namespace + "/" + w.Name
	if w.Cluster != "" {
		where = w.Cluster + " " + where
	}
	out := []string{"Workload: " + w.Kind + " " + where}
	from := func(path string) string { return " (from " + w.Source + ": " + path + ")" }
	if w.Image != nil {
		out = append(out, "  image "+w.Image.Value+from(w.Image.From))
	}
	for _, p := range w.Ports {
		out = append(out, "  port "+strings.Join(strings.Fields(strconv.Itoa(p.Port)+" "+p.Name+" "+p.Protocol), " ")+from(p.From))
	}
	for _, p := range w.Probes {
		line := "  " + p.Kind + " probe"
		if p.Path != "" {
			line += " " + p.Path
		}
		if p.Port != "" {
			line += " port " + p.Port
		}
		out = append(out, line+from(p.From))
	}
	return append(out, w.groupLines(from)...)
}

// groupLines renders the group and repositories of w: where Pacto read each,
// or why it stays ungrouped or its repository is unknown. The group's key is
// on a line of its own, so a person can copy it into the group filter.
func (w *InferredWorkload) groupLines(from func(string) string) []string {
	var out []string
	switch g := w.Group; {
	case g == nil:
	case g.Ambiguous != "":
		out = append(out, "  ungrouped: "+g.Ambiguous)
	default:
		out = append(out, "  group "+g.Label+from(g.From), "  group key "+g.Key+" (pacto fleet entities --group takes it)")
	}
	for _, r := range w.Repositories {
		line := "  repository " + r.URL
		if r.Path != "" {
			line += " path " + r.Path
		}
		out = append(out, line+" (from "+r.From+")")
	}
	if w.RepositoryUnknown != "" {
		out = append(out, "  repository unknown: "+w.RepositoryUnknown)
	}
	return out
}

// InferredLines renders the detail of an inferred service as text for the
// CLI and the TUI: its workload, the declared services it was seen calling,
// or whether none were seen or calls are unknown, and its limitations.
func (d *ServiceDetailData) InferredLines(asOf time.Time) []string {
	out := d.Workload.Lines()
	switch {
	case d.Calls.Total > 0:
		out = append(out, fmt.Sprintf("Calls (%d):", d.Calls.Total))
		for _, c := range d.Calls.Items {
			out = append(out, "  "+c.Label)
		}
		if d.Calls.Truncated {
			out = append(out, fmt.Sprintf("  %d more", d.Calls.Total-d.Calls.Count))
		}
	case d.HiddenDependencies > 0:
	case d.CallsObserved:
		out = append(out, "No calls to declared services seen as of "+asOf.Format(time.RFC3339))
	default:
		out = append(out, "Calls unknown: Pacto reads no traffic source here")
	}
	if d.HiddenDependencies > 0 {
		out = append(out, fmt.Sprintf("Calls to services you cannot see: %d", d.HiddenDependencies))
	}
	for _, l := range d.Limitations.Items {
		out = append(out, "  - ["+l.Limitation.Code+"] "+l.Limitation.Message)
	}
	if d.NextAction != nil {
		out = append(out, "Next: "+d.NextAction.CLI)
	}
	return out
}

// InferredServiceOf is the inferred service of one decoded workload object
// (Kind is Deployment, StatefulSet, DaemonSet or CronJob): its identity, its
// creation time and its shape, each value's path prefixed with the kind. The
// caller sets the group and repositories it can read.
func InferredServiceOf(kind string, obj map[string]any, now time.Time) RawInferredService {
	meta, _ := obj["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	ns, _ := meta["namespace"].(string)
	in := RawInferredService{Namespace: ns, Kind: kind, Name: name, ObservedAt: &now}
	if created, _ := meta["creationTimestamp"].(string); created != "" {
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			in.CreatedAt = &t
		}
	}
	shape := workloadshape.ReadWorkloadShape(kind, name, obj)
	if shape.Image != "" {
		in.Image = &InferredValue{Value: shape.Image, From: kind + " " + shape.ImageFrom}
	}
	for _, p := range shape.Ports {
		in.Ports = append(in.Ports, InferredPort{Port: p.Number, Name: p.Name, Protocol: p.Protocol, From: kind + " " + p.From})
	}
	for _, p := range shape.Probes {
		in.Probes = append(in.Probes, InferredProbe{Kind: p.Kind, Path: p.Path, Port: p.Port, From: kind + " " + p.From})
	}
	return in
}
