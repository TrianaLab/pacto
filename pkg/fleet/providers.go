package fleet

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LimitationProviderSourceDegraded: a detected-provider row is not available
// and says why, or took the lower status of the source that reported it.
const LimitationProviderSourceDegraded = "PROVIDER_SOURCE_DEGRADED"

// LimitationDetectionClusterMismatch: a detected row names a cluster none of
// the snapshot's Kubernetes sources reads, so it may describe another cluster.
// Its source reads stale.
const LimitationDetectionClusterMismatch = "DETECTION_CLUSTER_MISMATCH"

// ScopeCluster is the Scope of a detected row read cluster-wide; a reader who
// may not read the cluster scope sees it redacted.
const ScopeCluster = "cluster"

// Row ids and UsedFor values of the Kubernetes rows. An extender adds its own
// beside them; UsedFor stays a free string list, never an enum.
const (
	// ProviderPactoCRD is the row of the Pacto CRD a Kubernetes source discovered.
	ProviderPactoCRD = "pacto-crd"
	// ProviderKubernetesWorkloads is the row of the workloads a workload source listed.
	ProviderKubernetesWorkloads = "kubernetes-workloads"
	// UseInferredServices: Pacto infers services from what the row read.
	UseInferredServices = "inferred-services"
	// UseDeclaredTargets: Pacto reads declared targets from what the row read.
	UseDeclaredTargets = "declared-targets"
	// ProviderHelm is the row of the Helm releases a workload source grouped
	// workloads by, read from their release annotations.
	ProviderHelm = "helm"
	// HelmReleaseKind is the kind of the Helm row's counts. A release is read
	// from workload annotations, so a reader must be allowed to list every
	// workload kind in its namespace to see one.
	HelmReleaseKind = "Helm release"
	// UseServiceGrouping: Pacto groups inferred services by what the row read.
	UseServiceGrouping = "service-grouping"
	// ProviderArgoCD is the row of the Argo CD Applications a workload source
	// listed to name the repository behind a group.
	ProviderArgoCD = "argocd"
	// ProviderFlux is the row of the Flux Kustomizations, HelmReleases and
	// GitRepositories a workload source listed for the same reason.
	ProviderFlux = "flux"
	// UseRepositoryProvenance: Pacto names the repository a group deploys
	// from out of what the row read.
	UseRepositoryProvenance = "repository-provenance"
	// UsePromotion: the row's product promotes versions in this installation.
	// Its evidence starts with PromotionOwnedBy.
	UsePromotion = "promotion"
	// UseReporting: the row's product reports provisioning or usage to Pacto,
	// the reporting adapters row family.
	UseReporting = "reporting"
	// UseReportedTargets: the row's producer reports targets through an
	// Evidence Server.
	UseReportedTargets = "reported-targets"
	// UseContributedRecords: the row's producer contributes inferred
	// services through its signed records.
	UseContributedRecords = "contributed-records"
	// ProviderRegistryPrefix prefixes the id of the row of a registry host an
	// OCI source read: registry:ghcr.io.
	ProviderRegistryPrefix = "registry:"
	// UseNativeReferrers: Pacto attaches records there as OCI 1.1 referrers.
	UseNativeReferrers = "native-referrers"
	// UseWriteOnceTags: the registry serves no Referrers API, so Pacto writes
	// one write-once, content-addressed tag per record there.
	UseWriteOnceTags = "write-once-tags"
	// UseIndexListedByCatalog: Pacto lists repositories there through _catalog.
	UseIndexListedByCatalog = "index-listed-by-catalog"
	// UseIndexListedByTag: the registry refuses _catalog, so Pacto lists by tag.
	UseIndexListedByTag = "index-listed-by-tag"
	// ProviderKernelEgress is the row of the calls leaving the cluster that
	// the node agent saw, built from the Pacto statuses a source read.
	ProviderKernelEgress = "kernel-egress"
	// UseExternalEgress: Pacto reads calls leaving the cluster, to Externals,
	// from what the row read.
	UseExternalEgress = "external-egress"
)

// LimitationRegistryProbeFailed: Pacto could not probe a registry host, so it
// does not know which mode it would use there.
const LimitationRegistryProbeFailed = "REGISTRY_PROBE_FAILED"

// Row ids, UsedFor value and limitation codes of the service mesh rows.
const (
	// ProviderIstio is the row of the Istio mesh a mesh source found.
	ProviderIstio = "istio"
	// ProviderLinkerd is the row of the Linkerd mesh a mesh source found.
	ProviderLinkerd = "linkerd"
	// ProviderHubble is the row of the Cilium Hubble a mesh source found.
	ProviderHubble = "hubble"
	// UseObservedEdges: Pacto reads observed calls from what the row read.
	UseObservedEdges = "observed-edges"
	// LimitationMeshTelemetryUnread: a mesh was found but the calls it
	// records could not be read.
	LimitationMeshTelemetryUnread = "MESH_TELEMETRY_UNREAD"
	// LimitationMeshCoveragePartial: part of the cluster sends no calls
	// through the mesh, or Pacto cannot tell which part does.
	LimitationMeshCoveragePartial = "MESH_COVERAGE_PARTIAL"
	// LimitationMeshCallsUnattributed: calls the mesh recorded that Pacto
	// could not attribute to a workload of this cluster were left out.
	LimitationMeshCallsUnattributed = "MESH_CALLS_UNATTRIBUTED"
	// LimitationEgressSeriesCap: a mesh recorded more calls to external
	// hosts than Pacto reads in one window; only the busiest were kept.
	LimitationEgressSeriesCap = "EGRESS_SERIES_CAP"
)

// PromotionOwnedBy starts the evidence of every promotion row whose product
// was found, followed by the product: the phrase a delivery back-off reads. A
// row Pacto could not check is partial, says so and never starts with it.
const PromotionOwnedBy = "promotion owned by "

// A Provider is a system Pacto detected and read evidence from, never an
// offering provider. A source reports its rows in [Collection.Providers]; ids
// are per source, so every source may report a pacto-crd row.
//
// Extenders keep the shape: UsedFor is a free string list, never an enum;
// Evidence never names a namespace or a namespaced object (those go in
// Objects, which a restricted view narrows); evidence counted in namespaces
// goes in Counts and pkg/fleet words it through [WorkloadEvidence].
type Provider struct {
	ID           string           `json:"id" doc:"Stable id of the row within its source, for example pacto-crd or kubernetes-workloads"`
	Source       string           `json:"source" doc:"The id of the source that detected it"`
	Cluster      string           `json:"cluster,omitempty" doc:"The display name of the cluster the source read, when it reads one"`
	Name         string           `json:"name"`
	Evidence     string           `json:"evidence" doc:"What Pacto found. It never names a namespace or a namespaced object; those go in objects"`
	UsedFor      []string         `json:"usedFor" doc:"What Pacto uses it for, for example inferred-services or declared-targets"`
	Status       SourceStatus     `json:"status" enum:"available,partial,stale,unavailable"`
	Objects      []ProviderObject `json:"objects,omitempty" doc:"The objects the evidence names, at most 20"`
	ObjectsTotal int              `json:"objectsTotal,omitempty" doc:"How many objects the row had before the cap or your permissions narrowed it"`
	Limitations  []Limitation     `json:"limitations,omitempty"`
	Offers       []ProviderOffer  `json:"offers,omitempty" doc:"Pacto integrations that fit this provider, offered because it was detected; Pacto installs nothing"`
	// Provenance, AsOf, Hint, Details and Scope are set on a row Pacto
	// detected (see pkg/detect); a row a source reported directly leaves them
	// empty. The operator's Installation row, from the same detection, is the
	// one row with Provenance declared.
	Provenance string     `json:"provenance,omitempty" doc:"declared for the operator's Installation row (its configuration); inferred for a row Pacto detected; empty for a row a source reported directly"`
	AsOf       *time.Time `json:"asOf,omitempty" doc:"When the row was detected"`
	Hint       string     `json:"hint,omitempty" doc:"A command or value for a person to review that would turn the proposal on. Nothing is turned on until a person runs it"`
	Details    []string   `json:"details,omitempty" doc:"Further candidates or facts behind the row, at most 5"`
	Scope      string     `json:"scope,omitempty" enum:"cluster" doc:"cluster when the row was read cluster-wide"`
	// ClusterID is the id of the cluster a detected row was read in, read
	// only by Build's cluster check.
	ClusterID string `json:"-"`
	// Counts and UnreadableKinds are never serialised: Build renders Evidence
	// from them and Restrict renders it again from the visible namespaces. A
	// producer that read and found nothing passes a non-nil empty Counts.
	Counts          []ProviderCount `json:"-"`
	UnreadableKinds []string        `json:"-"`
	// GitOpsUndiscovered is set on a kubernetes-workloads row whose source
	// could not discover the Argo CD and Flux APIs, so the missing argocd and
	// flux rows are unknown, not absent. Never serialised.
	GitOpsUndiscovered bool `json:"-"`
	// Services are the services whose revisions a registry row's host served;
	// Restrict drops the row when every one is hidden. Never serialised.
	Services []ServiceKey `json:"-"`
	// producer is what a producer row was built from (see [ProducerRow]), so
	// Build and Restrict can build it again. Never serialised.
	producer *producerInput
}

// ProviderObject is one object a provider row's evidence names. Namespace is
// empty for a cluster-scoped object, which a restricted view hides.
type ProviderObject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// ProviderOffer is one Pacto integration a detected provider row offers.
type ProviderOffer struct {
	ID   string `json:"id" doc:"Stable offer id"`
	Text string `json:"text" doc:"One sentence, Gate every <thing> with <mechanism>"`
	Doc  string `json:"doc" doc:"The setup guide, an https URL"`
}

// ArgoSyncGateDoc is the setup guide of the Argo CD sync gate.
const ArgoSyncGateDoc = "https://pacto.run/latest/integrations/kubernetes/gitops/#gate-every-argo-cd-sync"

// providerOffers are the offers finalizeProviders puts on a row by its id.
var providerOffers = map[string][]ProviderOffer{
	ProviderArgoCD: {{ID: "argocd-repo-server-plugin", Text: "Gate every Argo CD sync with one repo-server plugin", Doc: ArgoSyncGateDoc}},
}

// rowOffers is p's offers merged by id with the table's for its id (the
// table wins, then the first of a duplicate), sorted by id, in a new slice. A
// row that is unavailable offers nothing, whatever set its offers.
func rowOffers(p *Provider) []ProviderOffer {
	if p.Status == SourceUnavailable {
		return nil
	}
	out := append(slices.Clone(providerOffers[p.ID]), p.Offers...)
	slices.SortStableFunc(out, func(a, b ProviderOffer) int { return cmp.Compare(a.ID, b.ID) })
	return slices.CompactFunc(out, func(a, b ProviderOffer) bool { return a.ID == b.ID })
}

// ProviderCount is how many objects of one kind a source read in one namespace.
type ProviderCount struct {
	Kind      string // singular Kubernetes kind, for example Deployment
	Namespace string
	N         int
}

// ingestProviders appends a copy of each row col reports, with its source and
// cluster set. The copy matters: a MemorySource hands back the same Collection
// on every Collect, so Build never writes one.
func ingestProviders(snap *FleetSnapshot, sourceID string, col *Collection) {
	cluster := ""
	if col.Cluster != nil {
		cluster = cmp.Or(col.Cluster.Name, col.Cluster.ID)
	}
	for _, p := range col.Providers {
		p.UsedFor, p.Objects, p.Limitations = slices.Clone(p.UsedFor), slices.Clone(p.Objects), slices.Clone(p.Limitations)
		p.Counts, p.UnreadableKinds = slices.Clone(p.Counts), slices.Clone(p.UnreadableKinds)
		p.Details, p.Services = slices.Clone(p.Details), slices.Clone(p.Services)
		if p.AsOf != nil {
			at := *p.AsOf
			p.AsOf = &at
		}
		// A row that names its own cluster (a detected row) keeps it when the
		// source reports none.
		p.Source, p.Cluster = sourceID, cmp.Or(cluster, p.Cluster)
		snap.Providers = append(snap.Providers, p)
	}
}

// checkDetectedClusters names, on each row with a ClusterID and in its
// evidence, the cluster the snapshot knows it by, or, when the snapshot holds
// clusters and none is that one, adds LimitationDetectionClusterMismatch and
// makes its available source stale. A snapshot with no cluster has nothing to compare with.
func checkDetectedClusters(snap *FleetSnapshot) {
	if len(snap.Clusters) == 0 {
		return
	}
	for i := range snap.Providers {
		p := &snap.Providers[i]
		if p.ClusterID == "" {
			continue
		}
		if j := slices.IndexFunc(snap.Clusters, func(c ClusterRecord) bool { return c.ID == p.ClusterID }); j >= 0 {
			// The evidence says the cluster the way the rest of the answer does.
			if name := snap.Clusters[j].Name; name != "" {
				p.Evidence = strings.Replace(p.Evidence, "cluster "+p.Cluster+")", "cluster "+name+")", 1)
				p.Cluster = name
			}
			continue
		}
		p.Limitations = append(p.Limitations, Limitation{Code: LimitationDetectionClusterMismatch, Source: p.Source,
			Message: "detected in cluster " + p.ClusterID + ", which no Kubernetes source of this answer reads"})
		for k := range snap.Sources {
			if snap.Sources[k].ID == p.Source && snap.Sources[k].Status == SourceAvailable {
				snap.Sources[k].Status = SourceStale
			}
		}
	}
}

// finalizeProviders runs once every source state is final. It adds the
// reporting adapter rows, drops the rows of an unavailable source, never lets
// a row claim better health than its source, gives every row that is not
// available a reason, renders counted evidence, sorts by source then id, keeps
// the first of a duplicate and caps the objects of each row.
func finalizeProviders(snap *FleetSnapshot) {
	snap.Providers = finalizeRows(append(snap.Providers, reportingRows(snap.Provisioning)...), sourceStates(snap.Sources))
}

// sourceStates is the least healthy status of each source id.
func sourceStates(sources []SourceState) map[string]SourceStatus {
	state := map[string]SourceStatus{}
	for _, st := range sources {
		if cur, ok := state[st.ID]; !ok || sourceHealthRank(st.Status) < sourceHealthRank(cur) {
			state[st.ID] = st.Status
		}
	}
	return state
}

// finalizeRows is finalizeProviders over rows and the source states.
func finalizeRows(rows []Provider, state map[string]SourceStatus) []Provider {
	rows = slices.DeleteFunc(rows, func(p Provider) bool { return state[p.Source] == SourceUnavailable })
	slices.SortStableFunc(rows, func(a, b Provider) int { return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID)) })
	rows = slices.CompactFunc(rows, func(a, b Provider) bool { return a.Source == b.Source && a.ID == b.ID })
	for i := range rows {
		p := &rows[i]
		src := state[p.Source]
		// A row that sets no status claims available, so taking its source's
		// lower status is a downgrade that says why.
		status, _ := canonicalSourceStatus(cmp.Or(p.Status, SourceAvailable))
		switch {
		case sourceHealthRank(src) < sourceHealthRank(status):
			status = src
			p.Limitations = append(p.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " is " + string(src) + "; this row covers only what it read"})
		case status != SourceAvailable && len(p.Limitations) == 0:
			p.Limitations = append(p.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " reported this row as " + string(status) + " and gave no reason"})
		}
		p.Status = status
		p.Offers = rowOffers(p)
		if p.Counts != nil || p.UnreadableKinds != nil {
			p.Evidence = countsEvidence(p.Counts, p.UnreadableKinds)
		}
		slices.SortFunc(p.Objects, func(a, b ProviderObject) int {
			return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})
		if len(p.Objects) > MaxProviderObjects {
			p.ObjectsTotal = max(p.ObjectsTotal, len(p.Objects))
			p.Objects = p.Objects[:MaxProviderObjects]
		}
	}
	return rows
}

// reportingAdapterID prefixes the id of every row reportingRows makes.
const reportingAdapterID = "reporting-adapter/"

// restrictedProviders is rows restricted by restrictProviders, with the
// reporting adapter rows made again from out's restricted provisioning
// reports, so a view never names an adapter or producer its reports hide. A
// row an evidence source reported from its trust config (a producer the
// installation allows to report) names no report and stays for every viewer:
// the Evidence Server lists those producers to anyone on GET /producers.
func restrictedProviders(out *FleetSnapshot, rows []Provider, hiddenSvc map[ServiceKey]bool, hiddenNames []string, visible func(cluster, namespace string) bool, workloadVisible func(cluster, namespace, kind string) bool, srcClusters map[string]string) []Provider {
	rows = slices.DeleteFunc(slices.Clone(rows), func(p Provider) bool { return strings.HasPrefix(p.ID, reportingAdapterID) })
	rows = restrictProducerRows(rows, out.Targets, sourceStates(out.Sources))
	rows = append(restrictProviders(rows, hiddenSvc, hiddenNames, visible, workloadVisible, out.Clusters, srcClusters), finalizeRows(reportingRows(out.Provisioning), sourceStates(out.Sources))...)
	slices.SortStableFunc(rows, func(a, b Provider) int { return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID)) })
	return rows
}

// reportingRows are the reporting adapter rows of recs, the matched
// provisioning reports: one per source and adapter, an adapter being the slug,
// producer and signing offeringAdapters groups reporters by, so a signed and
// an unsigned report of one producer are two rows.
func reportingRows(recs []ProvisioningRecord) []Provider {
	type rowKey struct {
		source, provider, producer string
		verified                   bool
	}
	seen := map[rowKey]bool{}
	var rows []Provider
	for _, r := range recs {
		k := rowKey{r.Source, r.Provider, r.Producer, r.Verified}
		if seen[k] {
			continue
		}
		seen[k] = true
		ev, id := "reports provisioning in the unsigned target-state file", reportingAdapterID+r.Provider+"/"+r.Producer
		if r.Verified {
			ev, id = "reports provisioning in signed envelopes", id+"/signed"
		}
		if r.Producer != "" {
			ev += " as producer " + r.Producer
		}
		rows = append(rows, Provider{ID: id, Source: r.Source,
			Name: cmp.Or(r.Provider, "unnamed adapter"), Evidence: ev, UsedFor: []string{UseReporting}})
	}
	return rows
}

// countsEvidence is WorkloadEvidence over per-namespace counts.
func countsEvidence(counts []ProviderCount, unreadable []string) string {
	byKind := map[string]int{}
	for _, c := range counts {
		byKind[c.Kind] += c.N
	}
	return WorkloadEvidence(byKind, unreadable)
}

// workloadKindOrder is the order WorkloadEvidence names the common kinds in;
// any other kind follows, sorted.
var workloadKindOrder = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}

// WorkloadEvidence is the one wording of counted evidence: "412 Deployments,
// 3 StatefulSets". A kind read with 0 is omitted, a kind that could not be
// read everywhere is "<Kind>s unreadable" (never 0), or "at least 12
// Deployments (some unreadable)" when some were read. When every kind was read
// and held nothing it says "no GitRepositories or Kustomizations found", or
// "no workloads found" when it names no kind other than the workload kinds.
func WorkloadEvidence(counts map[string]int, unreadable []string) string {
	kinds := map[string]bool{}
	for k := range counts {
		kinds[k] = true
	}
	for _, k := range unreadable {
		kinds[k] = true
	}
	rank := func(k string) int {
		if i := slices.Index(workloadKindOrder, k); i >= 0 {
			return i
		}
		return len(workloadKindOrder)
	}
	var parts, none []string
	for _, k := range slices.SortedFunc(maps.Keys(kinds), func(a, b string) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b)) }) {
		switch n := counts[k]; {
		case slices.Contains(unreadable, k) && n > 0:
			parts = append(parts, "at least "+kindCount(n, k)+" (some unreadable)")
		case slices.Contains(unreadable, k):
			parts = append(parts, PluralKind(k)+" unreadable")
		case n > 0:
			parts = append(parts, kindCount(n, k))
		case rank(k) == len(workloadKindOrder):
			none = append(none, PluralKind(k))
		}
	}
	switch {
	case len(parts) > 0:
		return strings.Join(parts, ", ")
	case len(none) > 0:
		return "no " + strings.Join(none, " or ") + " found"
	}
	return "no workloads found"
}

// kindCount is "1 Deployment" or "n Deployments".
func kindCount(n int, kind string) string {
	if n == 1 {
		return "1 " + kind
	}
	return strconv.Itoa(n) + " " + PluralKind(kind)
}

// PluralKind is the one plural of a kind: "GitRepositories", "Deployments".
func PluralKind(k string) string {
	if strings.HasSuffix(k, "y") && len(k) > 1 && !strings.ContainsRune("aeiouAEIOU", rune(k[len(k)-2])) {
		return k[:len(k)-1] + "ies"
	}
	return k + "s"
}

// restrictProviders is rows as a reader visible and workloadVisible say yes
// to sees them. Every row stays, except a registry row whose every service is
// in hiddenSvc and a declared row from a cluster the view hides; what changes
// is cloned. An object in a
// namespace the reader may not read goes, a cluster-scoped one included unless
// visible("") says yes, and ObjectsTotal keeps the count. A count stays only
// when the reader may read its namespace and list its kind there (a nil
// workloadVisible keeps none), and Evidence is rendered again with one hidden
// total. A cluster that clusters does not name goes from the row: the view
// hides it everywhere else. A limitation restrictScope would drop, or one that
// names a hidden name, goes, and so does every one of a row naming a hidden
// service; the degraded limitation names no namespace and stays. A row that is not available and lost every reason gets a degraded
// one, so it still says why. Evidence a producer wrote that names a hidden
// name is masked; evidence rendered from counts names none. Every namespace
// of a row is read in the row's cluster (see providerCluster; srcClusters maps
// source ids to cluster ids).
func restrictProviders(rows []Provider, hiddenSvc map[ServiceKey]bool, hiddenNames []string, visibleIn func(cluster, namespace string) bool, workloadVisibleIn func(cluster, namespace, kind string) bool, clusters []ClusterRecord, srcClusters map[string]string) []Provider {
	if rows == nil {
		return nil
	}
	mentionsHidden := func(s string) bool {
		return slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(s, n) })
	}
	shown := map[string]bool{}
	for _, c := range clusters {
		shown[c.ID], shown[c.Name] = true, true
	}
	// Offers stay shared with rows: finalize built them and nothing writes
	// them after, and their static text discloses nothing.
	out := make([]Provider, 0, len(rows))
	for i, p := range rows {
		if allServicesHidden(p, hiddenSvc) {
			continue
		}
		visible, workloadVisible := inCluster(providerCluster(p, srcClusters), visibleIn, workloadVisibleIn)
		if !hideCluster(&p, shown) {
			continue
		}
		p.Objects, p.ObjectsTotal = restrictObjects(p, visible, workloadVisible)
		hidden := map[string]int{}
		p.Counts = slices.DeleteFunc(slices.Clone(p.Counts), func(c ProviderCount) bool {
			if countVisible(c, visible, workloadVisible) {
				return false
			}
			hidden[c.Kind] += c.N
			return true
		})
		p.Evidence = restrictedEvidence(p, hidden, hiddenNames)
		if len(rows[i].Objects) > 0 && len(p.Objects) == 0 && !slices.ContainsFunc(rows[i].Objects, func(o ProviderObject) bool { return !workloadKind(o.Kind) }) {
			// Every object was a workload the reader may not list: the
			// evidence a producer wrote about them goes too.
			p.Evidence = p.Name + " found in a namespace you cannot read"
		}
		redactDetected(&p, len(rows[i].Objects), visible)
		redactEdgeCounts(&p, visible)
		// A row built over services the reader partly cannot see may owe its
		// reason to a hidden one: only the degraded limitation stays.
		someHidden := slices.ContainsFunc(p.Services, func(k ServiceKey) bool { return hiddenSvc[k] })
		p.Limitations = restrictRowLimitations(p, func(l Limitation) bool {
			return someHidden || hiddenByScope(l, visibleIn, srcClusters) || mentionsHidden(l.Message)
		})
		out = append(out, p)
	}
	return out
}

// restrictRowLimitations are p's limitations without those hidden reports
// hidden; the degraded limitation names no namespace and stays. A row that is
// not available and lost every reason gets a degraded one.
func restrictRowLimitations(p Provider, hidden func(Limitation) bool) []Limitation {
	out := slices.DeleteFunc(slices.Clone(p.Limitations), func(l Limitation) bool {
		return l.Code != LimitationProviderSourceDegraded && hidden(l)
	})
	if p.Status != SourceAvailable && len(out) == 0 {
		out = []Limitation{{Code: LimitationProviderSourceDegraded, Source: p.Source,
			Message: "source " + p.Source + " reported this row as " + string(p.Status) + "; the reason is hidden by your permissions"}}
	}
	return out
}

// allServicesHidden reports whether p names services and hiddenSvc hides
// every one of them, so the row goes with them.
func allServicesHidden(p Provider, hiddenSvc map[ServiceKey]bool) bool {
	return len(p.Services) > 0 && !slices.ContainsFunc(p.Services, func(k ServiceKey) bool { return !hiddenSvc[k] })
}

// hideCluster takes from p its cluster when shown does not name it, and
// reports whether p stays. A declared row goes whole: the Installation row's
// details and push error name the cluster's snapshot tag.
func hideCluster(p *Provider, shown map[string]bool) bool {
	if shown[p.Cluster] {
		return true
	}
	if p.Provenance == ProvenanceDeclared && p.Cluster != "" {
		return false
	}
	p.Evidence, p.Limitations = hideDetectedCluster(*p)
	p.Cluster = ""
	return true
}

// hideDetectedCluster is the evidence and limitations of p without the
// cluster a detected row names: the evidence loses its ", cluster <name>"
// and the cluster mismatch limitation goes.
func hideDetectedCluster(p Provider) (string, []Limitation) {
	if p.Provenance != ProvenanceInferred || p.Cluster == "" {
		return p.Evidence, p.Limitations
	}
	return strings.Replace(p.Evidence, ", cluster "+p.Cluster+")", ")", 1),
		slices.DeleteFunc(slices.Clone(p.Limitations), func(l Limitation) bool { return l.Code == LimitationDetectionClusterMismatch })
}

// redactDetected redacts a row p a reader visible says yes to may not read
// whole, its objects already narrowed from had: a cluster-wide row of any
// provenance for a reader who may not read the cluster scope, and a detected
// row that lost an object to the view or to the cap. Its evidence then says only that it
// exists, and its hint and details go, so the view never repeats what the
// operator read with grants the reader lacks.
func redactDetected(p *Provider, had int, visible func(string) bool) {
	switch {
	case p.Scope == ScopeCluster && !visible(""):
		// Whatever its provenance: a source-reported cluster-wide row (kernel
		// egress) counts nodes the reader may not see either.
		p.Evidence = p.Name + " detected cluster-wide; you may read only some namespaces"
		p.Hint, p.Details = "", nil
	case p.Provenance != ProvenanceInferred:
	case len(p.Objects) < max(p.ObjectsTotal, had):
		p.Evidence = p.Name + " found in a namespace you cannot read"
		p.Hint, p.Details = "", nil
	}
}

// EdgeCountText is how a live edge source's row says how many edges it read
// (see redactEdgeCounts).
func EdgeCountText(n int) string {
	if n == 1 {
		return "1 edge in the last 15 minutes"
	}
	return strconv.Itoa(n) + " edges in the last 15 minutes"
}

// edgeCountHidden replaces [EdgeCountText] for a reader who may read only
// some namespaces.
const edgeCountHidden = "edge count hidden by your permissions"

// redactEdgeCounts takes from a live edge source's row p the figures it
// counted over the whole fleet, for a reader visible says may read only some
// namespaces (see edgeCountsHidden): the edge count in its evidence, its details (calls, senders
// and what the winner misses) and each reason that starts with a count. The
// figures would say how busy the services the reader cannot see are.
func redactEdgeCounts(p *Provider, visible func(string) bool) {
	if !edgeCountsHidden(*p, visible) {
		return
	}
	parts := strings.Split(p.Evidence, "; ")
	for i, part := range parts {
		figure, _, _ := strings.Cut(part, " ")
		if n, err := strconv.Atoi(figure); err == nil && part == EdgeCountText(n) {
			parts[i] = edgeCountHidden
		}
	}
	p.Evidence, p.Details = strings.Join(parts, "; "), nil
	p.Limitations = slices.Clone(p.Limitations)
	for i := range p.Limitations {
		if l := &p.Limitations[i]; l.Code == LimitationEdgeSourcePartial {
			l.Message = strings.Join(slices.DeleteFunc(strings.Split(l.Message, "; "), startsWithCount), "; ")
		}
	}
	p.Limitations = slices.DeleteFunc(p.Limitations, func(l Limitation) bool { return l.Message == "" })
}

// edgeCountsHidden reports whether a reader visible says yes to sees p, a
// live edge source's row, without its figures: a reader who may read only
// some namespaces.
func edgeCountsHidden(p Provider, visible func(string) bool) bool {
	return !visible("") && slices.Contains(p.UsedFor, UseObservedEdges)
}

// startsWithCount reports whether s starts with a digit.
func startsWithCount(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// detectedNarrowed reports whether a reader visible says yes to would see p
// redacted by redactDetected while losing none of its objects.
func detectedNarrowed(p Provider, visible func(string) bool) bool {
	return !visible("") && (p.Scope == ScopeCluster || p.Provenance == ProvenanceInferred && p.ObjectsTotal > len(p.Objects))
}

// restrictedEvidence is the evidence of p, already narrowed, with the counts
// hidden per kind left out: counted evidence is rendered again with the hidden
// total, and evidence a producer wrote is masked when it names a hidden name.
// A row left with only zero counts says how many are hidden instead of none
// found, so it never says both.
func restrictedEvidence(p Provider, hidden map[string]int, hiddenNames []string) string {
	total := 0
	for _, n := range hidden {
		total += n
	}
	suffix := " (+" + strconv.Itoa(total) + " hidden by your permissions)"
	switch {
	case p.Counts == nil && p.UnreadableKinds == nil:
		if slices.ContainsFunc(hiddenNames, func(n string) bool { return mentionsWord(p.Evidence, n) }) {
			return "hidden by your permissions"
		}
		return p.Evidence
	case total == 0:
		return countsEvidence(p.Counts, p.UnreadableKinds)
	case len(p.UnreadableKinds) > 0:
	case len(p.Counts) == 0:
		return "none you may see" + suffix
	case !slices.ContainsFunc(p.Counts, func(c ProviderCount) bool { return c.N > 0 }):
		return WorkloadEvidence(hidden, nil) + " in namespaces hidden from you"
	}
	return countsEvidence(p.Counts, p.UnreadableKinds) + suffix
}

// mentionsWord reports whether s holds n as a whole name: not inside a longer
// one, so a hidden namespace "pacto" leaves "pacto.trianalab.io" alone.
func mentionsWord(s, n string) bool {
	if n == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], n)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(n)
		if (start == 0 || !nameByte(s[start-1])) && (end == len(s) || !nameByte(s[end])) {
			return true
		}
		i = start + 1
	}
}

// nameByte reports whether b may appear inside a Kubernetes or service name.
func nameByte(b byte) bool {
	return strings.IndexByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._", b) >= 0
}

// restrictObjects is the objects of p a reader may see and the row's objects
// total: an object needs read of its namespace (the cluster scope for one
// with none), and a workload also list of its kind there. A hidden workload
// goes uncounted, since a count of workloads is workload data too; any other
// hidden object keeps the total.
// ponytail: a capped row that loses a workload drops its total, so a list cut
// at MaxProviderObjects may then read whole; count the visible workloads
// before the cap if a row ever needs both.
func restrictObjects(p Provider, visible func(string) bool, workloadVisible func(namespace, kind string) bool) ([]ProviderObject, int) {
	kept := slices.DeleteFunc(slices.Clone(p.Objects), func(o ProviderObject) bool { return !objectVisible(o, visible, workloadVisible) })
	workloads := 0
	for _, o := range p.Objects {
		if workloadKind(o.Kind) && !objectVisible(o, visible, workloadVisible) {
			workloads++
		}
	}
	switch other := len(p.Objects) - len(kept) - workloads; {
	case workloads > 0 && other > 0:
		return kept, len(kept) + other
	case workloads > 0:
		return kept, 0
	case other > 0:
		return kept, cmp.Or(p.ObjectsTotal, len(p.Objects))
	}
	return kept, p.ObjectsTotal
}

// workloadKind reports whether kind is one of the workload kinds an inferred
// service runs as.
func workloadKind(kind string) bool { return slices.Contains(workloadKindOrder, kind) }

// objectVisible reports whether a reader may see o: read of its namespace,
// and for a workload list of its kind there (a nil workloadVisible lists
// nothing).
func objectVisible(o ProviderObject, visible func(string) bool, workloadVisible func(namespace, kind string) bool) bool {
	return visible(o.Namespace) && (!workloadKind(o.Kind) || workloadVisible != nil && workloadVisible(o.Namespace, o.Kind))
}

// countVisible reports whether a reader may see c: a zero count discloses
// nothing, any other needs read of its namespace and list of its kind there.
// A nil workloadVisible lists nothing.
func countVisible(c ProviderCount, visible func(string) bool, workloadVisible func(namespace, kind string) bool) bool {
	return c.N == 0 || visible(c.Namespace) && workloadVisible != nil && workloadVisible(c.Namespace, c.Kind)
}

// inCluster is visible and workloadVisible with their cluster fixed to
// cluster. A nil workloadVisible stays nil.
func inCluster(cluster string, visible func(cluster, namespace string) bool, workloadVisible func(cluster, namespace, kind string) bool) (func(string) bool, func(string, string) bool) {
	vis := func(ns string) bool { return visible(cluster, ns) }
	if workloadVisible == nil {
		return vis, nil
	}
	return vis, func(ns, kind string) bool { return workloadVisible(cluster, ns, kind) }
}

// providersNarrowed reports whether a reader visible and workloadVisible say
// yes to would lose an object or a count of some row of s, or see a detected
// row redacted. srcClusters maps source ids to cluster ids.
func providersNarrowed(s *FleetSnapshot, visibleIn func(cluster, namespace string) bool, workloadVisibleIn func(cluster, namespace, kind string) bool, srcClusters map[string]string) bool {
	return slices.ContainsFunc(s.Providers, func(p Provider) bool {
		visible, workloadVisible := inCluster(providerCluster(p, srcClusters), visibleIn, workloadVisibleIn)
		return detectedNarrowed(p, visible) || edgeCountsHidden(p, visible) || slices.ContainsFunc(p.Objects, func(o ProviderObject) bool { return !objectVisible(o, visible, workloadVisible) }) ||
			slices.ContainsFunc(p.Counts, func(c ProviderCount) bool { return !countVisible(c, visible, workloadVisible) })
	})
}

// boundProviders is a copy of rows, the rows a source reported before the
// inferred ones, each least healthy first then by source and id, capped at
// MaxProviders.
func boundProviders(rows []Provider) ([]Provider, bool) {
	out := slices.Clone(rows)
	inferred := func(p Provider) int {
		if p.Provenance == ProvenanceInferred {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(out, func(a, b Provider) int {
		return cmp.Or(cmp.Compare(inferred(a), inferred(b)), cmp.Compare(sourceHealthRank(a.Status), sourceHealthRank(b.Status)))
	})
	if len(out) <= MaxProviders {
		return out, false
	}
	return out[:MaxProviders], true
}

// ProvidersResult answers "what did Pacto detect": every provider row of the
// query's snapshot, bounded, with the product meta that names each source.
// The Kubernetes facts come from every source, never from the bounded meta.
type ProvidersResult struct {
	Meta              ProductMeta   `json:"meta"`
	Total             int           `json:"total" doc:"How many rows the snapshot holds before the bound"`
	Truncated         bool          `json:"truncated,omitempty"`
	Providers         []Provider    `json:"providers" doc:"At most 200 rows, reported before inferred, least healthy first, then by source and id"`
	KubernetesSources int           `json:"kubernetesSources" doc:"How many Kubernetes and workload sources the view reads, the unavailable ones included"`
	NotRead           []SourceState `json:"notRead" doc:"The Kubernetes and workload sources Pacto could not read, at most 50"`
	NotReadTotal      int           `json:"notReadTotal" doc:"How many Kubernetes and workload sources Pacto could not read before the bound"`
}

// Providers lists the detected-provider rows of the query, reported before
// inferred and least healthy first.
func (q *Query) Providers() *ProvidersResult {
	rows, truncated := boundProviders(q.snap.Providers)
	if rows == nil {
		rows = []Provider{}
	}
	res := &ProvidersResult{Meta: q.productMeta(), Total: len(q.snap.Providers), Truncated: truncated, Providers: rows, NotRead: []SourceState{}}
	for _, st := range q.snap.Sources {
		if st.Kind != SourceKindKubernetes && st.Kind != SourceKindKubernetesWorkloads {
			continue
		}
		res.KubernetesSources++
		if st.Status == SourceUnavailable {
			res.NotReadTotal++
			if len(res.NotRead) < MaxMetaSources {
				res.NotRead = append(res.NotRead, st)
			}
		}
	}
	res.NotRead = cloneSources(res.NotRead)
	return res
}
