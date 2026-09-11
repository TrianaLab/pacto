package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// K8sSource is a live [fleet.Source] over Pacto CRs in a Kubernetes cluster. It
// projects each CR's operator-computed status (compliance, findings, coverage,
// observed runtime, resolved ref) into a fleet target. The operator is the
// observer; this source only reads what it already reconciled. Each CR is a
// concrete deployed instance, so it becomes a target (never a revision).
type K8sSource struct {
	id        string
	client    k8sclient.K8sClient
	namespace string // "" means all namespaces
}

// NewK8sSource returns a live cluster source reading Pacto CRs in namespace
// (empty for all namespaces). id is the provenance id (e.g. the cluster or
// context name).
func NewK8sSource(id string, client k8sclient.K8sClient, namespace string) *K8sSource {
	if id == "" {
		id = "k8s"
	}
	return &K8sSource{id: id, client: client, namespace: namespace}
}

// ContractRefs returns the contract references the Pacto CRs in namespace
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
func ContractRefs(ctx context.Context, client k8sclient.K8sClient, namespace string) ([]string, error) {
	col, err := NewK8sSource("k8s", client, namespace).Collect(ctx)
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
	if i := strings.LastIndex(ref, "@"); i > 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon]
	}
	return ref
}

// ID implements [fleet.Source].
func (s *K8sSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *K8sSource) Kind() string { return "kubernetes" }

// Collect discovers the Pacto CRD, lists its resources in the configured
// namespace, and projects each into a target. A cluster with no Pacto CRD
// installed yields an empty collection (reachable, nothing declared) rather
// than an error; an unreachable cluster or a failed list is a source-level
// error so [fleet.Build] records it as unavailable.
func (s *K8sSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	disc, err := s.client.DiscoverCRD(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovering pacto crd: %w", err)
	}
	if !disc.Found {
		return &fleet.Collection{}, nil
	}
	resource := disc.ResourceName
	if resource == "" {
		resource = "pactos"
	}
	data, err := s.client.ListJSON(ctx, resource, s.namespace)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", resource, err)
	}
	var list k8sList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decoding %s list: %w", resource, err)
	}
	col := &fleet.Collection{}
	for _, item := range list.Items {
		col.Targets = append(col.Targets, s.targetFrom(item))
	}
	return col, nil
}

// targetFrom projects one CR into a fleet target. It maps only status the
// operator already computed; it never re-evaluates.
func (s *K8sSource) targetFrom(item k8sItem) fleet.RawTarget {
	st := item.Status
	service := item.Metadata.Name
	resolvedRef := ""
	if st.Contract != nil {
		if st.Contract.ServiceName != "" {
			service = st.Contract.ServiceName
		}
		resolvedRef = st.Contract.ResolvedRef
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
		Domain:          OciDomain(resolvedRef),
		Kind:            "kubernetes",
		Name:            item.Metadata.Name,
		Service:         service,
		ResolvedRef:     resolvedRef,
		Digest:          digestFromRef(resolvedRef),
		Compliance:      st.ContractStatus,
		Findings:        findingsFromK8s(st.Findings),
		Readiness:       st.Readiness.result(),
		ObservedRuntime: st.ObservedRuntime,
		EvidenceAt:      observed,
		ReconciledAt:    observed,
	}
	if st.EvaluationCoverage != nil {
		t.Coverage = &fleet.Coverage{
			Evaluated: int(st.EvaluationCoverage.Evaluated),
			Required:  int(st.EvaluationCoverage.Required),
		}
	}
	return t
}

// digestFromRef extracts the @sha256:... digest from a resolved OCI ref, or ""
// when the ref carries no digest.
func digestFromRef(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ""
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
			refs = append(refs, finding.EvidenceRef{Source: r.Source, ObservedAt: r.ObservedAt})
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
	Status   k8sStatus   `json:"status"`
}

type k8sMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type k8sStatus struct {
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
}
