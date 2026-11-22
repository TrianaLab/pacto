package fleet

import (
	"slices"
	"strings"
	"time"
)

// Provenance and completeness of an installation part. host is a fact the
// answering process states about itself.
const (
	InstallationProvenanceHost   = "host"
	InstallationComplete         = "complete"
	InstallationPartial          = "partial"
	InstallationUnknown          = "unknown"
	InstallationApplierAdopted   = "adopted"
	InstallationEngineUnknown    = "unknown"
	installationRowID            = "installation"
	installationHidden           = "the fleet snapshot in your view holds no Installation row for this cluster: your view hides it, or the snapshot predates the detection"
	installationNoQuery          = "no fleet snapshot in your view: none is published yet, or your permissions could not be checked"
	installationNoDetected       = "no --detected: what the operator was configured with is not read here"
	installationNoBlock          = "the detection carries no installation block"
	installationNoProfile        = "the operator does not advertise its profile"
	installationNoDelivery       = "this server reads no delivery"
	installationNotProbed        = "delivery is served but whether the cluster serves its API is not read here"
	installationInProcess        = "this server serves delivery itself"
	installationProbed           = "the delivery probe of this server"
	installationProvisioning     = "no source here detects a provisioning engine"
	installationJoinsNotRead     = "; cluster join requests are not read here"
	installationClustersInView   = "the clusters in your view of the fleet snapshot"
	installationApplierEvidence  = "Pacto delivery adopts workloads it owns per target"
	installationApplierNotListed = " row is not available, so no applier is listed from it"
	installationAppliersNoQuery  = "; " + installationNoQuery + ", so appliers are not read here"
	installationNoWorkloads      = "; no workload source in your view reads this cluster's Argo CD and Flux objects, so appliers are not read here"
	installationUndiscovered     = "; Pacto could not discover the Argo CD and Flux APIs, so appliers are not read here"
	installationDeliveryKept     = "; the declared profile hardened renders no Pacto delivery, so the API is served by CRDs Helm kept and no applier is listed"
)

// applyingKinds are the Argo CD and Flux kinds that apply workloads: a row
// that counts none of them names no applier.
var applyingKinds = []string{"Application", "Kustomization", "HelmRelease"}

// InstallationDeclared is what the operator was configured with, read from
// its detection's Installation block: declared by a person, never inferred.
type InstallationDeclared struct {
	// Profile is nil from an operator that does not advertise it.
	Profile  []string
	Registry string
	// ClusterID is the cluster the detection was read in; the answer names
	// the block only to a caller whose view still holds its Installation row.
	ClusterID string
	AsOf      time.Time
	Source    string
}

// InstallationInput is what a host knows when it answers: the declared block
// and the state of the detection it came from (nil when no detection is
// read), its raw delivery probe and whether it serves MCP.
type InstallationInput struct {
	Declared      *InstallationDeclared
	DeclaredState *SourceState
	// DeliveryWired: the host serves delivery. DeliveryProbed: it asked
	// whether the cluster serves the delivery API, with DeliveryInstalled
	// and DeliveryErr the raw answer. DeliveryInProcess: the host serves
	// delivery itself, so the answer is its own.
	DeliveryWired     bool
	DeliveryProbed    bool
	DeliveryInProcess bool
	DeliveryInstalled bool
	DeliveryErr       error
	// DeliveryAsOf is when the probe last asked the cluster (it caches its
	// answer); zero reads as Now.
	DeliveryAsOf time.Time
	MCPServed    bool
	MCPSource    string
	// JoinSource, when set, says where the cluster join requests were read
	// (the registry repository) or why JoinPending is null; JoinPending is
	// how many of them the caller may see wait for a person, and JoinAsOf
	// when they were read. Empty: this host reads no join requests.
	JoinPending *int
	JoinSource  string
	JoinAsOf    *time.Time
	Now         time.Time
}

// Installation is how this installation runs, each part with where Pacto
// learned it, when, its provenance and how complete it is. Unknown is null,
// never a positive.
type Installation struct {
	Profile      InstallationProfile      `json:"profile" doc:"The profile names the chart rendered, declared by a person"`
	Registry     InstallationRegistry     `json:"registry" doc:"The registry value the operator was configured with, declared by a person"`
	Delivery     InstallationDelivery     `json:"delivery" doc:"Whether the cluster serves the Pacto delivery API, observed by this host, and the appliers inferred from detected rows"`
	Provisioning InstallationProvisioning `json:"provisioning" doc:"The provisioning engine, unknown until a source detects one"`
	Clusters     InstallationClusters     `json:"clusters" doc:"The clusters this caller's view reads"`
	MCP          InstallationMCP          `json:"mcp" doc:"Whether this host serves an MCP endpoint"`
}

// InstallationProfile is the declared profile.
type InstallationProfile struct {
	Value        []string   `json:"value" doc:"Sorted profile names; null when unknown"`
	Source       string     `json:"source" doc:"Where Pacto read it, or why it is null"`
	AsOf         *time.Time `json:"asOf" doc:"When it was read"`
	Provenance   string     `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string     `json:"completeness" enum:"complete,partial,unknown"`
}

// InstallationRegistry is the declared registry value.
type InstallationRegistry struct {
	Value        *string    `json:"value" doc:"The registry value; empty when set to nothing, null when unknown"`
	Source       string     `json:"source" doc:"Where Pacto read it, or why it is null"`
	AsOf         *time.Time `json:"asOf" doc:"When it was read"`
	Provenance   string     `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string     `json:"completeness" enum:"complete,partial,unknown"`
}

// InstallationDelivery says whether the cluster serves the Pacto delivery API.
// The delivery CRDs outlive a chart that dropped delivery (Helm keeps them),
// so a served API is not proof a delivery controller runs.
type InstallationDelivery struct {
	Enabled      *bool                 `json:"enabled" doc:"The cluster serves the Pacto delivery API; null when this host cannot tell. The CRDs outlive a chart that dropped delivery, so this is partial beside a declared hardened profile"`
	Appliers     []InstallationApplier `json:"appliers" nullable:"false" doc:"What applies the workloads Pacto delivery promotes, inferred from available detected rows; listed only when enabled is true"`
	Source       string                `json:"source" doc:"Where Pacto learned it, or why it is null"`
	AsOf         *time.Time            `json:"asOf" doc:"When it was read"`
	Provenance   string                `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string                `json:"completeness" enum:"complete,partial,unknown"`
}

// InstallationApplier is one tool that applies what Pacto delivery promotes.
type InstallationApplier struct {
	Manager    string `json:"manager" doc:"The detected row's id: argocd or flux"`
	Mode       string `json:"mode" enum:"adopted,idp-flux"`
	Provenance string `json:"provenance" enum:"declared,observed,inferred,host"`
	Source     string `json:"source" doc:"The row Pacto inferred it from"`
}

// InstallationProvisioning is the provisioning engine.
type InstallationProvisioning struct {
	Engine       string     `json:"engine" enum:"crossplane,adapter,none,unknown"`
	Source       string     `json:"source" doc:"Where Pacto learned it, or why it is unknown"`
	AsOf         *time.Time `json:"asOf" doc:"When it was read"`
	Provenance   string     `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string     `json:"completeness" enum:"complete,partial,unknown"`
}

// InstallationClusters counts the clusters of the caller's view.
type InstallationClusters struct {
	Joined       *int       `json:"joined" doc:"Clusters this caller's view reads; null with no snapshot"`
	Pending      *int       `json:"pending" doc:"Clusters waiting for a person to approve them as members, as this caller may see them; null when not read, when some are hidden from this caller or when the join list was read partly"`
	Source       string     `json:"source" doc:"Where Pacto counted them, or why they are null"`
	AsOf         *time.Time `json:"asOf" doc:"When the snapshot was generated"`
	Provenance   string     `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string     `json:"completeness" enum:"complete,partial,unknown"`
}

// InstallationMCP says whether this host serves MCP.
type InstallationMCP struct {
	Served       bool       `json:"served" doc:"This host serves an MCP endpoint"`
	Source       string     `json:"source" doc:"Which host says so"`
	AsOf         *time.Time `json:"asOf" doc:"When it was answered"`
	Provenance   string     `json:"provenance" enum:"declared,observed,inferred,host"`
	Completeness string     `json:"completeness" enum:"complete,partial,unknown"`
}

// Installation answers how the installation runs, on q, the caller's own view
// (nil when no snapshot is published or the caller could not be checked).
// The dashboard and MCP both call it, so they cannot disagree.
func (q *Query) Installation(in InstallationInput) Installation {
	d, source, completeness := q.declared(in)
	now := in.Now
	return Installation{
		Profile:      installationProfile(d, source, completeness),
		Registry:     installationRegistry(d, source, completeness),
		Delivery:     q.installationDelivery(in, d),
		Provisioning: InstallationProvisioning{Engine: InstallationEngineUnknown, Source: installationProvisioning, AsOf: &now, Provenance: ProvenanceInferred, Completeness: InstallationUnknown},
		Clusters:     q.installationClusters(in),
		MCP:          InstallationMCP{Served: in.MCPServed, Source: in.MCPSource, AsOf: &now, Provenance: InstallationProvenanceHost, Completeness: InstallationComplete},
	}
}

// declared is the declared block the caller may read, or nil with why, and
// the completeness of what it answers.
func (q *Query) declared(in InstallationInput) (*InstallationDeclared, string, string) {
	st, d := in.DeclaredState, in.Declared
	switch {
	case st == nil:
		return nil, installationNoDetected, InstallationUnknown
	case st.Status == SourceUnavailable:
		msg := "the detection could not be read"
		if st.Error != nil {
			msg += ": " + st.Error.Message
		}
		return nil, msg, InstallationUnknown
	case d == nil:
		return nil, installationNoBlock, InstallationUnknown
	case q == nil:
		return nil, installationNoQuery, InstallationUnknown
	case !q.holdsInstallationRow(d.ClusterID):
		return nil, installationHidden, InstallationUnknown
	case st.Status != SourceAvailable:
		return d, d.Source + " (" + string(st.Status) + ")", InstallationPartial
	}
	return d, d.Source, InstallationComplete
}

// holdsInstallationRow reports whether q still carries the Installation row
// of cluster: Restrict drops it from a view that hides the cluster.
func (q *Query) holdsInstallationRow(cluster string) bool {
	clusters := clusterOfSource(q.snap)
	return slices.ContainsFunc(q.snap.Providers, func(p Provider) bool {
		return p.ID == installationRowID && providerCluster(p, clusters) == cluster
	})
}

func installationProfile(d *InstallationDeclared, source, completeness string) InstallationProfile {
	p := InstallationProfile{Source: source, Provenance: ProvenanceDeclared, Completeness: completeness}
	switch {
	case d == nil:
	case d.Profile == nil:
		p.Source, p.Completeness, p.AsOf = installationNoProfile, InstallationUnknown, &d.AsOf
	default:
		p.Value, p.AsOf = slices.Clone(d.Profile), &d.AsOf
	}
	return p
}

func installationRegistry(d *InstallationDeclared, source, completeness string) InstallationRegistry {
	r := InstallationRegistry{Source: source, Provenance: ProvenanceDeclared, Completeness: completeness}
	if d != nil {
		reg := d.Registry
		r.Value, r.AsOf = &reg, &d.AsOf
	}
	return r
}

// installationDelivery is the host's delivery probe, never reading a failed
// or missing probe as a positive, with the appliers when the API is served.
// d is the declared block the caller may read, or nil.
func (q *Query) installationDelivery(in InstallationInput, d *InstallationDeclared) InstallationDelivery {
	asOf := in.Now
	if !in.DeliveryAsOf.IsZero() {
		asOf = in.DeliveryAsOf
	}
	out := InstallationDelivery{Appliers: []InstallationApplier{}, AsOf: &asOf, Provenance: ProvenanceObserved, Completeness: InstallationUnknown}
	switch {
	case !in.DeliveryWired:
		out.Source = installationNoDelivery
		return out
	case !in.DeliveryProbed:
		out.Source = installationNotProbed
		return out
	case in.DeliveryErr != nil:
		out.Source = "the delivery probe failed: " + in.DeliveryErr.Error()
		return out
	}
	enabled := in.DeliveryInstalled
	out.Enabled, out.Source, out.Completeness = &enabled, installationProbed, InstallationComplete
	if in.DeliveryInProcess {
		out.Source = installationInProcess
	}
	switch {
	case !enabled:
	case d != nil && slices.Contains(d.Profile, "hardened"):
		out.notRead(installationDeliveryKept)
	default:
		cluster := ""
		if d != nil {
			cluster = d.ClusterID
		}
		q.addAppliers(&out, cluster)
	}
	return out
}

// notRead makes the part partial and adds note to its source once.
func (out *InstallationDelivery) notRead(note string) {
	out.Completeness = InstallationPartial
	if !strings.Contains(out.Source, note) {
		out.Source += note
	}
}

// addAppliers lists one adopted applier per manager whose available argocd or
// flux row counts an object of an applying kind, in cluster when it is known
// (else in every cluster of the view). A row that is not available, no
// workload source to read the rows, or a failed discovery lists nothing and
// makes the part partial: an unread input is never an empty list.
func (q *Query) addAppliers(out *InstallationDelivery, cluster string) {
	if q == nil {
		out.notRead(installationAppliersNoQuery)
		return
	}
	clusters := clusterOfSource(q.snap)
	read := false
	for _, p := range q.snap.Providers {
		if cluster != "" && providerCluster(p, clusters) != cluster {
			continue
		}
		switch {
		case p.ID == ProviderKubernetesWorkloads && p.GitOpsUndiscovered:
			out.notRead(installationUndiscovered)
		case p.ID == ProviderKubernetesWorkloads:
			read = true
		case p.ID == ProviderArgoCD || p.ID == ProviderFlux:
			out.addApplier(p)
		}
	}
	if !read && !strings.Contains(out.Source, installationUndiscovered) {
		out.notRead(installationNoWorkloads)
	}
}

// addApplier lists p's manager once when p is available and counts an object
// of an applying kind: a served CRD with nothing in it applies nothing.
func (out *InstallationDelivery) addApplier(p Provider) {
	switch {
	case p.Status != SourceAvailable:
		out.notRead("; " + p.ID + installationApplierNotListed)
	case !slices.ContainsFunc(p.Counts, func(c ProviderCount) bool { return c.N > 0 && slices.Contains(applyingKinds, c.Kind) }):
	case slices.ContainsFunc(out.Appliers, func(a InstallationApplier) bool { return a.Manager == p.ID }):
		// one row per cluster names the same manager
	default:
		out.Appliers = append(out.Appliers, InstallationApplier{Manager: p.ID, Mode: InstallationApplierAdopted, Provenance: ProvenanceInferred,
			Source: "detected " + p.ID + " row; " + installationApplierEvidence})
	}
}

// installationClusters counts the clusters of the caller's snapshot and the
// join requests in.JoinPending counts. A join count that is null beside a
// joined count makes the part partial.
func (q *Query) installationClusters(in InstallationInput) InstallationClusters {
	joins := installationJoinsNotRead
	if in.JoinSource != "" {
		joins = "; cluster join requests: " + in.JoinSource
	}
	out := InstallationClusters{Pending: in.JoinPending, AsOf: in.JoinAsOf, Source: installationNoQuery + joins, Provenance: ProvenanceObserved, Completeness: InstallationUnknown}
	if q == nil {
		return out
	}
	n, at := len(q.snap.Clusters), q.snap.GeneratedAt
	out.Joined, out.AsOf, out.Source, out.Completeness = &n, &at, installationClustersInView+joins, InstallationComplete
	if q.snap.Completeness == CompletenessPartial || in.JoinSource != "" && in.JoinPending == nil {
		out.Completeness = InstallationPartial
	}
	return out
}
