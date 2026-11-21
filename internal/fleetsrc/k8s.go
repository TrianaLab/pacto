package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/contract"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/readiness"
)

// K8sSource is a live [fleet.Source] over Pacto CRs in a Kubernetes cluster. It
// projects each CR's operator-computed status (compliance, findings, coverage,
// observed runtime, resolved ref) into a fleet target. The operator is the
// observer; this source only reads what it already reconciled. Each CR is a
// concrete deployed instance, so it becomes a target (never a revision).
type K8sSource struct {
	id         string
	cluster    *fleet.Cluster // nil when the cluster identity could not be read
	client     k8sclient.K8sClient
	namespaces []string // sorted, deduplicated; empty means all namespaces
}

// NewK8sSource returns a live cluster source reading Pacto CRs in namespaces
// (empty for all namespaces). id is the provenance id (the cluster UID, else
// the context name); cluster is the cluster every target is in, nil when
// unknown.
func NewK8sSource(id string, cluster *fleet.Cluster, client k8sclient.K8sClient, namespaces []string) *K8sSource {
	return &K8sSource{id: cmp.Or(id, "k8s"), cluster: cluster, client: client, namespaces: normalizeNamespaces(namespaces)}
}

// ContractRefs returns the contract references the Pacto CRs in namespaces
// (empty for all namespaces) currently name: for every CR with a non-empty
// status.contract.resolvedRef, that exact ref AND the repository it names,
// deduplicated and sorted.
//
// Both are needed, and they answer different questions. The exact ref names the
// revision a target is RUNNING -- immutable when the operator pinned a digest,
// and the only reference that can make a target's revision link exact rather
// than inferred. The repository resolves to the newest PUBLISHED revision, which
// is what makes "a newer revision exists" answerable at all. Reporting only
// repositories drops the running revision from the graph whenever it is not also
// the latest tag -- precisely the case an operator pins a digest to create.
//
// The discovery, listing and error conventions are [K8sSource.Collect]'s: a
// cluster with no Pacto CRD installed yields no references rather than an error.
func ContractRefs(ctx context.Context, client k8sclient.K8sClient, namespaces []string) ([]string, error) {
	col, err := NewK8sSource("k8s", nil, client, namespaces).Collect(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, 2*len(col.Targets))
	for _, t := range col.Targets {
		for _, ref := range []string{t.ResolvedRef, repoFromRef(t.ResolvedRef)} {
			if ref != "" {
				seen[ref] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// repoFromRef strips the digest then the tag from a resolved OCI ref, leaving
// the repository. A ':' is only a tag separator in the final path segment, so a
// registry port (localhost:5000/svc) survives.
func repoFromRef(ref string) string {
	// The last '@' and never a leading one: a leading '@' is not a digest
	// separator, which is the one place this differs from contract.SplitOCIRef.
	if i := strings.LastIndex(ref, "@"); i > 0 {
		ref = ref[:i]
	}
	repo, _, _ := contract.SplitOCITag(ref)
	return repo
}

// ID implements [fleet.Source].
func (s *K8sSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *K8sSource) Kind() string { return fleet.SourceKindKubernetes }

// Cluster implements [fleet.ClusterSource]: nil when the identity could not
// be read.
func (s *K8sSource) Cluster() *fleet.Cluster { return s.cluster }

// Collect discovers the Pacto CRD, lists its resources in each configured
// namespace (or once across all of them), and projects each into a target. A
// cluster with no Pacto CRD installed yields no target and a
// PACTO_CRD_NOT_INSTALLED limitation rather than an error: without Pacto CRs
// Pacto cannot tell where a service runs. An unreachable cluster, or a list
// that fails in every namespace, is a source-level error so [fleet.Build]
// records it as unavailable.
//
// With a namespace list the source is partial whatever it finds, the
// CRD-missing case included: a service in any other namespace is unknown,
// never absent. A namespace whose list fails is left out of the namespaces it
// reports and named in its own limitation; the others are kept.
func (s *K8sSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	disc, err := s.client.DiscoverCRD(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovering pacto crd: %w", err)
	}
	if !disc.Found {
		missing := fleet.Limitation{
			Code: fleet.LimitationPactoCRDNotInstalled, Source: s.id,
			Message: "the Pacto CRD is not installed in this cluster, so Pacto reads no declared target here; its workloads show as inferred services",
		}
		return scoped(s.id, s.namespaces, &fleet.Collection{Cluster: s.cluster, Limitations: []fleet.Limitation{missing}, Providers: []fleet.Provider{{
			ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "not installed", UsedFor: []string{fleet.UseDeclaredTargets},
			Status: fleet.SourcePartial, Limitations: []fleet.Limitation{missing},
		}}}, s.namespaces), nil
	}
	col := &fleet.Collection{ObservedUndeclaredOnly: true, Cluster: s.cluster}
	namespaces := s.namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}
	var read []string
	var listErr error
	for _, ns := range namespaces {
		data, err := s.client.ListJSON(ctx, *disc, ns)
		if err != nil {
			listErr = cmp.Or(listErr, fmt.Errorf("listing %s: %w", disc.ResourceName, err))
			col.Limitations = append(col.Limitations, fleet.NamespaceUnreadableLimitation(s.id, ns, reasonClass(err)))
			continue
		}
		if err := s.collectList(data, disc.ResourceName, col); err != nil {
			return nil, err
		}
		read = append(read, ns)
	}
	if len(read) == 0 {
		return nil, listErr
	}
	col.Providers = append(col.Providers, crdServedRow(disc, col.Limitations))
	return scoped(s.id, s.namespaces, col, read), nil
}

// crdServedRow is the Pacto CRD row of a served CRD. Its evidence carries no
// count: the count is the source's targetCount, which a restricted view
// recounts. Its status is left to the source; it keeps the unreadable
// namespaces of lims.
func crdServedRow(disc *k8sclient.CRDDiscovery, lims []fleet.Limitation) fleet.Provider {
	return fleet.Provider{
		ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "served (" + k8sclient.PactoGroup + "/" + disc.Version + ")",
		UsedFor: []string{fleet.UseDeclaredTargets},
		Objects: []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: disc.ResourceName + "." + k8sclient.PactoGroup}},
		Limitations: slices.DeleteFunc(slices.Clone(lims), func(l fleet.Limitation) bool {
			return l.Code != fleet.LimitationK8sNamespaceUnreadable
		}),
	}
}

// scoped declares col of source id partial with the namespaces read, and
// states the scope, when the source reads a namespace list (namespaces); an
// unscoped col is returned as is.
func scoped(id string, namespaces []string, col *fleet.Collection, read []string) *fleet.Collection {
	if len(namespaces) == 0 {
		return col
	}
	col.State = &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: read, NamespacesTotal: len(read)}
	col.Limitations = append(col.Limitations, fleet.NamespaceScopeLimitation(id, read, 0))
	return col
}

// reasonClass is the class of a failed namespace list, never its raw text.
func reasonClass(err error) string {
	switch {
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsNotFound(err):
		return "not found"
	default:
		return "error"
	}
}

// collectList decodes one list of Pacto CRs into col.
func (s *K8sSource) collectList(data []byte, resource string, col *fleet.Collection) error {
	var list k8sList
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("decoding %s list: %w", resource, err)
	}
	// The same bytes decoded into list above, so they decode here too.
	var raw struct {
		Items []json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(data, &raw)
	nameless := 0
	for i, item := range list.Items {
		if c := item.Status.Contract; c != nil && c.ServiceName == "" {
			// The operator refuses a contract that does not declare its name, so
			// a reconciled one without it is a record this source cannot place.
			// The message names neither namespace nor Pacto: the item becomes no
			// target, so a restricted view could not tell it is hidden.
			nameless++
			continue
		}
		t := s.targetFrom(item)
		if v, ok := s.runtimeView(raw.Items[i], item, col); ok {
			col.Observed = append(col.Observed, delivery.ObservedEdges(v)...)
			col.ExternalUses = append(col.ExternalUses, delivery.ExternalUses(v, t.ReconciledAt)...)
			t.Limitations = s.runtimeLimitations(v, t.ReconciledAt)
		} else if c := item.Status.Contract; c != nil && c.ServiceName != "" {
			t.Limitations = unreadableLimitations(s.id, c.ServiceName)
		}
		col.Targets = append(col.Targets, t)
	}
	if nameless > 0 {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: fleet.LimitationSourceRecordInvalid, Source: s.id,
			Message: fmt.Sprintf("%d Pacto(s) report a contract with no service name", nameless),
		})
	}
	return nil
}

// LimitationPactoStatusUnreadable marks a Pacto whose runtime observation (its
// observed callers and their coverage) does not decode. Its target is still
// collected; its runtime calls and the external systems it uses are missing.
const LimitationPactoStatusUnreadable = "PACTO_STATUS_UNREADABLE"

// runtimeView decodes what the operator's runtime observation wrote into a
// reconciled Pacto's status. ok is false for a Pacto with no reconciled contract,
// and for one whose status does not decode, which adds a limitation to col.
func (s *K8sSource) runtimeView(raw json.RawMessage, item k8sItem, col *fleet.Collection) (delivery.PactoView, bool) {
	var v delivery.PactoView
	if item.Status.Contract == nil || item.Status.Contract.ServiceName == "" {
		return v, false
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: LimitationPactoStatusUnreadable, Source: s.id,
			Message: "the runtime observation of Pacto " + item.Metadata.Namespace + "/" + item.Metadata.Name + " could not be read; its runtime calls and the external systems it uses are missing",
		})
		return v, false
	}
	return v, true
}

// unreadableLimitations are the target limitations of a reconciled Pacto
// whose status does not decode: neither its callers nor the external systems
// it uses can be ruled out, so no answer reads its silence as "none".
func unreadableLimitations(source, service string) []fleet.Limitation {
	why := ": the operator's status for it could not be read (status-unreadable)"
	return []fleet.Limitation{
		{Code: delivery.LimitationCallersUnseen, Source: source, Message: "runtime callers of " + service + " cannot be ruled out" + why},
		{Code: fleet.LimitationExternalsUnreported, Source: source, Message: "the external systems " + service + " uses cannot be ruled out" + why},
	}
}

// runtimeLimitations are [delivery.CallerLimitations] and
// [delivery.ExternalLimitations], attributed to this source.
func (s *K8sSource) runtimeLimitations(v delivery.PactoView, reconciled *time.Time) []fleet.Limitation {
	now := time.Now()
	lims := append(delivery.CallerLimitations(v, reconciled, now), delivery.ExternalLimitations(v, reconciled, now)...)
	for i := range lims {
		lims[i].Source = s.id
	}
	return lims
}

// targetFrom projects one CR into a fleet target. It maps only status the
// operator already computed; it never re-evaluates.
func (s *K8sSource) targetFrom(item k8sItem) fleet.RawTarget {
	st := item.Status
	service := item.Metadata.Name
	resolvedRef := ""
	var owner contract.Owner
	var businessDomain, offering, summary string
	// status.contract.domain is omitted when empty, so its absence says the
	// contract declares none only on a status format that records the domain.
	domainKnown := st.Contract != nil && st.StatusFormat >= delivery.StatusFormatDomain
	if st.Contract != nil {
		businessDomain = st.Contract.Domain
		offering, summary = st.Contract.Offering, st.Contract.Summary
		service = st.Contract.ServiceName
		resolvedRef = st.Contract.ResolvedRef
		if st.Contract.Owner != nil {
			owner = *st.Contract.Owner
		}
	}
	// The operator inspects the live cluster and evaluates it in one reconcile
	// pass, so lastReconciledAt answers both questions the fleet asks separately of
	// an ingested EvidenceSet: when the environment was OBSERVED (EvidenceAt) and
	// when Pacto reconciled that observation (ReconciledAt). Leaving EvidenceAt nil
	// made the record contradict itself -- it carried the operator's coverage and
	// findings under an "no evidence has been observed" limitation -- and put every
	// Kubernetes target permanently outside the freshness window, so an operator
	// wedged for a month still read as freshly observed.
	observed := parseK8sTime(st.LastReconciledAt)
	t := fleet.RawTarget{
		Scope: item.Metadata.Namespace,
		// Domain is derived from the resolved OCI reference the same way the OCI and
		// cache sources derive it, so a service's runtime target correlates with its
		// published baseline (same registry+org → same logical service) instead of
		// splitting into a k8s-only and an OCI-only service.
		Domain:              contract.OCIDomain(resolvedRef),
		Kind:                fleet.SourceKindKubernetes,
		Name:                item.Metadata.Name,
		Labels:              environmentLabel(item.Metadata.Labels),
		Service:             service,
		ResolvedRef:         resolvedRef,
		Digest:              digestFromRef(resolvedRef),
		Compliance:          st.ContractStatus,
		Owner:               owner,
		BusinessDomain:      businessDomain,
		BusinessDomainKnown: domainKnown,
		Offering:            offering,
		Summary:             summary,
		Findings:            findingsFromK8s(st.Findings),
		Readiness:           st.Readiness.result(),
		ObservedRuntime:     st.ObservedRuntime,
		EvidenceAt:          observed,
		ReconciledAt:        observed,
		CreatedAt:           parseK8sTime(item.Metadata.CreationTimestamp),
	}
	t.Workload = item.Spec.Target.Workload()
	if st.EvaluationCoverage != nil {
		t.Coverage = &fleet.Coverage{
			Evaluated: int(st.EvaluationCoverage.Evaluated),
			Required:  int(st.EvaluationCoverage.Required),
		}
	}
	return t
}

// environmentLabel keeps only the environment label of a Pacto: the fleet places a
// target by it, and the CR's other labels are not the fleet's to publish.
func environmentLabel(labels map[string]string) map[string]string {
	if v, ok := labels[fleet.TargetEnvironmentLabel]; ok {
		return map[string]string{fleet.TargetEnvironmentLabel: v}
	}
	return nil
}

// digestFromRef extracts the @sha256:... digest from a resolved OCI ref, or ""
// when the ref carries no digest.
func digestFromRef(ref string) string {
	_, digest := contract.SplitOCIDigest(ref)
	return digest
}

// parseK8sTime parses an RFC3339 timestamp, returning nil when absent or
// unparseable (freshness then falls back to other signals).
func parseK8sTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// findingsFromK8s maps operator finding status into engine findings, preserving
// severity, category, subject, path, message and evidence provenance.
func findingsFromK8s(in []k8sFinding) []finding.Finding {
	if len(in) == 0 {
		return nil
	}
	out := make([]finding.Finding, 0, len(in))
	for _, f := range in {
		var refs []finding.EvidenceRef
		for _, r := range f.EvidenceRefs {
			refs = append(refs, finding.EvidenceRef{Source: r.Source, ObservedAt: r.ObservedAt, Collector: r.Collector, DetectedAt: r.DetectedAt})
		}
		out = append(out, finding.Finding{
			Code:         finding.Code(f.Code),
			Severity:     finding.Severity(f.Severity),
			Category:     finding.Category(f.Category),
			Subject:      finding.SubjectRef{Name: f.Subject},
			ContractPath: f.ContractPath,
			Message:      f.Message,
			EvidenceRefs: refs,
		})
	}
	return out
}

// The parse structs below capture only the CR status fields the fleet needs.
// They mirror the operator's JSON tags (integrations/kubernetes/api) but stay
// decoupled: the fleet source reads status as data, not as an imported type.
type k8sList struct {
	Items []k8sItem `json:"items"`
}

type k8sItem struct {
	Metadata k8sMetadata `json:"metadata"`
	Spec     k8sSpec     `json:"spec"`
	Status   k8sStatus   `json:"status"`
}

// k8sSpec decodes spec.target: the Service and workload a Pacto observes.
type k8sSpec struct {
	Target *delivery.PactoTargetView `json:"target"`
}

type k8sMetadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels"`
	CreationTimestamp string            `json:"creationTimestamp"`
}

type k8sStatus struct {
	// StatusFormat is status.statusFormat (see delivery.StatusFormatDomain).
	StatusFormat       int            `json:"statusFormat"`
	ContractStatus     string         `json:"contractStatus"`
	Contract           *k8sContract   `json:"contract"`
	EvaluationCoverage *k8sCoverage   `json:"evaluationCoverage"`
	Findings           []k8sFinding   `json:"findings"`
	ObservedRuntime    map[string]any `json:"observedRuntime"`
	LastReconciledAt   string         `json:"lastReconciledAt"`
	Readiness          *k8sReadiness  `json:"readiness"`
}

// k8sReadiness decodes status.readiness. The operator derives that block FROM
// [readiness.Result] and publishes its fields under the same names, so the
// result is embedded and decoded into directly rather than re-listing thirteen
// fields a fourth time -- a hand-written mapping table here would be one more
// copy to drift. Only the rename needs stating: the wire calls the per-claim
// results "claims", the derived type calls them Checks.
//
// The operator publishes no partialCredit, so Result.PartialCredit stays zero:
// the fraction a partial claim earned is not recoverable from the status, and
// inventing the library default would assert a number the cluster never said.
type k8sReadiness struct {
	readiness.Result
	Claims []readiness.CheckResult `json:"claims"`
}

func (r *k8sReadiness) result() *readiness.Result {
	if r == nil {
		return nil
	}
	res := r.Result
	res.Checks = r.Claims
	return &res
}

type k8sContract struct {
	ServiceName string `json:"serviceName"`
	ResolvedRef string `json:"resolvedRef"`
	// Owner is status.contract.owner. The operator publishes it with the
	// contract's own field names (team, dri, contacts), so it decodes directly.
	Owner *contract.Owner `json:"owner"`
	// Domain is status.contract.domain, the contract's service.domain.
	Domain string `json:"domain"`
	// Offering and Summary are status.contract.offering and .summary, the
	// contract's service.offering and service.summary.
	Offering string `json:"offering"`
	Summary  string `json:"summary"`
}

type k8sCoverage struct {
	Evaluated int32 `json:"evaluated"`
	Required  int32 `json:"required"`
}

type k8sFinding struct {
	Code         string           `json:"code"`
	Severity     string           `json:"severity"`
	Category     string           `json:"category"`
	Subject      string           `json:"subject"`
	ContractPath string           `json:"contractPath"`
	Message      string           `json:"message"`
	EvidenceRefs []k8sEvidenceRef `json:"evidenceRefs"`
}

type k8sEvidenceRef struct {
	Source     string `json:"source"`
	ObservedAt string `json:"observedAt"`
	Collector  string `json:"collector"`
	DetectedAt string `json:"detectedAt"`
}
