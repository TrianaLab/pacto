package delivery

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// PactoView is the stable read contract of the Pacto custom resource: exactly
// the fields of a Pacto object the promoter reads, under the JSON names the
// operator writes. Decode a Pacto object (typed or unstructured) into it as
// JSON. The operator module tests that its typed status still fills every field
// here, so a rename there fails a test instead of silently starving delivery.
type PactoView struct {
	Metadata struct {
		Namespace  string            `json:"namespace"`
		Name       string            `json:"name"`
		Generation int64             `json:"generation"`
		Labels     map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Target *PactoTargetView `json:"target"`
	} `json:"spec"`
	Status PactoStatusView `json:"status"`
}

// PactoTargetView is spec.target: the Service and workload the Pacto observes.
type PactoTargetView struct {
	ServiceName string           `json:"serviceName"`
	WorkloadRef *WorkloadRefView `json:"workloadRef"`
}

// WorkloadRefView is spec.target.workloadRef.
type WorkloadRefView struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Workload is the workload the target names, with the CRD's documented
// default: the Deployment named like the Service. Nil when t is nil or names
// no workload.
func (t *PactoTargetView) Workload() *fleet.WorkloadRef {
	if t == nil {
		return nil
	}
	ref := fleet.WorkloadRef{Kind: "Deployment", Name: t.ServiceName}
	if w := t.WorkloadRef; w != nil {
		ref = fleet.WorkloadRef{Kind: cmp.Or(w.Kind, ref.Kind), Name: cmp.Or(w.Name, ref.Name)}
	}
	if ref.Name == "" {
		return nil
	}
	return &ref
}

// StatusFormatGuarantees is the first Pacto.status.statusFormat: the status
// records interface guarantees and dependency requirements. A status without
// statusFormat was written by an older operator and records neither, so what
// it would say about them is unknown.
const StatusFormatGuarantees = 1

// StatusFormatDomain is the statusFormat whose status.contract records the
// contract's business domain (service.domain). The field is omitted when empty,
// so only a status in this format or later says, by its absence, that the
// contract declares none; on an older one the domain is unknown.
const StatusFormatDomain = 2

// StatusFormatExternals is the first statusFormat that records the external
// systems the target workload names (status.externals) and how far that list
// can be trusted (status.externalsCoverage). On an older status which
// external systems the workload uses is unknown.
const StatusFormatExternals = 3

// StatusFormatEgress is the first statusFormat that records the external
// systems the node agent saw the target workload call (status.observedEgress)
// and how far that list can be trusted (status.observedEgressCoverage).
const StatusFormatEgress = 4

// PactoStatusView is the part of Pacto.status the promoter reads.
type PactoStatusView struct {
	// StatusFormat is the layout the operator wrote the status in (see
	// StatusFormatGuarantees); 0 when an older operator wrote it.
	StatusFormat       int               `json:"statusFormat"`
	ContractStatus     string            `json:"contractStatus"`
	ObservedGeneration int64             `json:"observedGeneration"`
	LastReconciledAt   string            `json:"lastReconciledAt"`
	Contract           *ContractInfoView `json:"contract"`
	// Interfaces are status.interfaces. The operator writes them under the
	// contract's own field names, guarantees, lifecycle and epoch included.
	Interfaces        []contract.Interface `json:"interfaces"`
	Dependencies      []DependencyView     `json:"dependencies"`
	EvaluatedRevision *EvaluatedView       `json:"evaluatedRevision"`
	Findings          []FindingView        `json:"findings"`
	// ObservedCallers are workloads the node agent saw calling the target's
	// Service that no Pacto describes.
	ObservedCallers []ObservedCallerView `json:"observedCallers"`
	// ObservedCallersCoverage says whether ObservedCallers is complete; nil
	// when runtime observation is not configured for this Pacto.
	ObservedCallersCoverage *CallerCoverageView `json:"observedCallersCoverage"`
	// Externals are the systems outside the cluster that the target
	// workload's pod spec names (status format 3).
	Externals []ExternalView `json:"externals"`
	// ExternalsCoverage says whether Externals is complete; nil on an older
	// status or one the operator could not evaluate.
	ExternalsCoverage *CallerCoverageView `json:"externalsCoverage"`
	// ExternalCatalog is the digest of the catalog Externals were classified
	// with, followed by " (embedded)" for the copy compiled into the operator.
	ExternalCatalog string `json:"externalCatalog"`
	// ObservedEgress are the systems outside the cluster the node agent saw
	// the target workload call (status format 4).
	ObservedEgress []EgressView `json:"observedEgress"`
	// ObservedEgressCoverage says whether ObservedEgress is complete; nil
	// when runtime observation is not configured for this Pacto.
	ObservedEgressCoverage *EgressCoverageView `json:"observedEgressCoverage"`
}

// EgressView is one Pacto.status.observedEgress entry: an external system
// reached by Host, never a path or a value.
type EgressView struct {
	Identity string `json:"identity"`
	Class    string `json:"class"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Source   string `json:"source"`
}

// EgressCoverageView is Pacto.status.observedEgressCoverage: the caller
// coverage fields and the nodes running pods, with how many run the node
// agent.
type EgressCoverageView struct {
	CallerCoverageView
	Nodes          int32 `json:"nodes"`
	NodesWithAgent int32 `json:"nodesWithAgent"`
}

// ExternalView is one Pacto.status.externals entry. Where names an object
// and a key and Host is a normalized host: the status carries no value.
type ExternalView struct {
	Identity   string `json:"identity"`
	Class      string `json:"class"`
	Confidence string `json:"confidence"`
	Kind       string `json:"kind"`
	Where      string `json:"where"`
	Host       string `json:"host"`
	Entry      string `json:"entry"`
}

// ObservedCallerView is one Pacto.status.observedCallers entry.
type ObservedCallerView struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Source    string `json:"source"`
}

// CallerCoverageView is Pacto.status.observedCallersCoverage.
type CallerCoverageView struct {
	State          string `json:"state"`
	Reason         string `json:"reason"`
	RefreshSeconds int32  `json:"refreshSeconds"`
}

// FindingView is one Pacto.status.findings entry.
type FindingView struct {
	Code         string            `json:"code"`
	Severity     string            `json:"severity"`
	Subject      string            `json:"subject"`
	Message      string            `json:"message"`
	EvidenceRefs []EvidenceRefView `json:"evidenceRefs"`
}

// EvidenceRefView is the part of a finding's evidence reference delivery
// cites: which collector saw it and when.
type EvidenceRefView struct {
	Collector  string `json:"collector"`
	DetectedAt string `json:"detectedAt"`
}

// ContractInfoView is Pacto.status.contract.
type ContractInfoView struct {
	ServiceName string          `json:"serviceName"`
	Version     string          `json:"version"`
	Owner       *contract.Owner `json:"owner"`
	ResolvedRef string          `json:"resolvedRef"`
}

// DependencyView is one Pacto.status.dependencies entry.
type DependencyView struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
	External      bool   `json:"external,omitempty"`
	Version       string `json:"version,omitempty"`
	// Requires is what the dependency relies on from the provider. A status
	// written by an operator older than 6.4.0 does not record it.
	Requires []contract.Requirement `json:"requires,omitempty"`
}

// EvaluatedView is Pacto.status.evaluatedRevision: the exact contract and
// workload generation a check result belongs to.
type EvaluatedView struct {
	ContractDigest string       `json:"contractDigest"`
	Workload       WorkloadView `json:"workload"`
	// KernelCoverage is how far the node agent's view of the workload could be
	// trusted in that evaluation: Complete, Partial, Failed or Stale, empty
	// when kernel evidence is off.
	KernelCoverage string `json:"kernelCoverage"`
}

// WorkloadView identifies one generation of one workload object.
type WorkloadView struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Generation int64  `json:"generation"`
}

// DecodePactos decodes a JSON list of Pacto objects ({"items": [...]}).
func DecodePactos(data []byte) ([]PactoView, error) {
	var list struct {
		Items []PactoView `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// SnapshotSourceID is the fleet source the delivery snapshot reports.
const SnapshotSourceID = "delivery"

// Snapshot builds the operational graph plan() projects a change onto: the
// Pactos' [PactoCollection] under [SnapshotSourceID], plus the revisions on
// both sides of the change.
func Snapshot(ctx context.Context, pactos []PactoView, listErr error, sides []Revision, now time.Time) (*fleet.FleetSnapshot, error) {
	col := PactoCollection(SnapshotSourceID, pactos, listErr, now)
	var revs []fleet.RawRevision
	for _, side := range sides {
		if side.Bundle == nil {
			continue
		}
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		revs = append(revs, fleet.RawRevision{
			Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef),
			RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + digest, Digest: digest,
		})
	}
	// The sides first, as before the collection was shared.
	col.Revisions = append(revs, col.Revisions...)
	src := fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col)
	// No ExternalCatalog: Build classifies with externalcatalog.Default(), the
	// catalog the operator compiles in, so its digest is the snapshot's catalog
	// ref that EXTERNAL_CATALOG_MISMATCH compares each status against.
	return fleet.Build(ctx, fleet.BuildOptions{Now: func() time.Time { return now }}, src)
}

// PactoCollection is the collection of the Pactos as source reads them: one
// revision and one target per reconciled Pacto (the revision rebuilt from the
// status the operator already wrote, so no registry read is needed per
// consumer), their observed edges and external uses. Every limitation names
// source.
//
// A Pacto the operator has not reconciled yet carries no contract in its status.
// It is reported as a limitation, which makes the collection partial: a consumer
// Pacto cannot read is a consumer it cannot rule out.
//
// listErr is the error listing the Pactos, if any: Pacto cannot rule out a
// consumer it could not list, so it makes the collection partial too.
func PactoCollection(source string, pactos []PactoView, listErr error, now time.Time) *fleet.Collection {
	col := &fleet.Collection{}
	if listErr != nil {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: "PACTOS_UNREADABLE", Source: source, Message: "the Pacto objects could not be listed: " + listErr.Error(),
		})
	}
	for _, p := range pactos {
		c := p.Status.Contract
		if c == nil || c.ServiceName == "" {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: "PACTO_NOT_RECONCILED", Source: source,
				Message: "Pacto " + p.Metadata.Namespace + "/" + p.Metadata.Name + " has no reconciled contract yet",
			})
			continue
		}
		rev, digest := revisionFromStatus(p)
		col.Revisions = append(col.Revisions, rev)
		reconciled := parseTime(p.Status.LastReconciledAt)
		lims := RuntimeLimitations(p, reconciled, now)
		for i := range lims {
			lims[i].Source = source
		}
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope: p.Metadata.Namespace, Kind: fleet.SourceKindKubernetes, Name: p.Metadata.Name,
			Service: c.ServiceName, Domain: rev.Domain, ResolvedRef: c.ResolvedRef, Digest: digest,
			Compliance: p.Status.ContractStatus, EvidenceAt: reconciled, ReconciledAt: reconciled,
			Limitations: lims, Workload: p.Spec.Target.Workload(),
		})
		col.Observed = append(col.Observed, ObservedEdges(p)...)
		col.ExternalUses = append(col.ExternalUses, ExternalUses(p, reconciled)...)
	}
	return col
}

// LimitationCallersUnseen marks a target whose runtime callers Pacto cannot
// rule out: its caller coverage is not complete, or its status stopped
// updating. It stays on the target, so the rest of the snapshot stays complete.
const LimitationCallersUnseen = "OBSERVED_CALLERS_UNSEEN"

// CallerLimitations says when the Pacto's runtime callers cannot be ruled out.
// A coverage the operator has not rewritten within twice its refresh interval
// is stale whatever it says: an operator that stopped would otherwise leave a
// complete coverage in place forever. No coverage means runtime observation is
// not configured, which changes nothing. p must carry status.contract.
func CallerLimitations(p PactoView, reconciled *time.Time, now time.Time) []fleet.Limitation {
	cov := p.Status.ObservedCallersCoverage
	if cov == nil {
		return nil
	}
	state, reason := freshCoverage(*cov, reconciled, now)
	if state == "complete" {
		return nil
	}
	return []fleet.Limitation{{Code: LimitationCallersUnseen, Source: SnapshotSourceID,
		Message: "runtime callers of " + p.Status.Contract.ServiceName + " cannot be ruled out: " + coverageWhy(state, reason)}}
}

// freshCoverage is the state and reason of cov, or unavailable with
// stale-status when the operator has not rewritten it in time (see
// staleStatus).
func freshCoverage(cov CallerCoverageView, reconciled *time.Time, now time.Time) (state, reason string) {
	if staleStatus(reconciled, cov.RefreshSeconds, now) {
		return "unavailable", "stale-status"
	}
	return cov.State, cov.Reason
}

// staleStatus reports whether a coverage the operator should rewrite every
// refreshSeconds is older than twice that: an operator that stopped would
// otherwise leave a complete coverage in place forever.
func staleStatus(reconciled *time.Time, refreshSeconds int32, now time.Time) bool {
	return reconciled == nil || refreshSeconds <= 0 || now.Sub(*reconciled) > 2*time.Duration(refreshSeconds)*time.Second
}

// coverageWhy is a coverage reason in plain words, citing state and slug.
func coverageWhy(state, reason string) string {
	return cmp.Or(coverageReasons[reason], "the operator reported "+reason) + " (coverage: " + state + ", " + reason + ")"
}

// ExternalUses are the external systems a Pacto's status says its target
// workload names, one use per status.externals entry, classified with the
// catalog status.externalCatalog names and observed when the operator last
// reconciled. p must carry status.contract.
func ExternalUses(p PactoView, reconciled *time.Time) []fleet.RawExternalUse {
	c := p.Status.Contract
	catalog := strings.TrimSuffix(p.Status.ExternalCatalog, " (embedded)")
	var out []fleet.RawExternalUse
	for _, e := range p.Status.Externals {
		out = append(out, fleet.RawExternalUse{
			Service: c.ServiceName, Domain: contract.OCIDomain(c.ResolvedRef), Namespace: p.Metadata.Namespace,
			Identity: e.Identity, Class: e.Class, Confidence: e.Confidence, Kind: e.Kind, Where: e.Where,
			Host: e.Host, Entry: e.Entry, Catalog: catalog, ObservedAt: reconciled,
		})
	}
	return out
}

// ExternalLimitations says when the external systems a Pacto's target uses
// cannot be listed in full: an older operator's status, a status format 3
// with no coverage, a coverage that is not complete or one gone stale (the
// rule of [CallerLimitations]). It stays on the target, so the rest of the
// snapshot stays complete, while every external answer turns partial. p must
// carry status.contract.
func ExternalLimitations(p PactoView, reconciled *time.Time, now time.Time) []fleet.Limitation {
	state, reason := externalsCoverage(p.Status, reconciled, now)
	if state == "complete" {
		return nil
	}
	return []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Source: SnapshotSourceID,
		Message: "the external systems " + p.Status.Contract.ServiceName + " uses cannot be ruled out: " + coverageWhy(state, reason)}}
}

// externalsCoverage is the state and reason of a status's externals list.
func externalsCoverage(s PactoStatusView, reconciled *time.Time, now time.Time) (state, reason string) {
	cov := s.ExternalsCoverage
	switch {
	case s.StatusFormat < StatusFormatExternals:
		return "unavailable", "status-format"
	case cov == nil:
		return "unavailable", "not-evaluated"
	case staleStatus(reconciled, cov.RefreshSeconds, now):
		return "unavailable", "stale-status"
	}
	return cov.State, cov.Reason
}

// coverageReasons says in plain words what each coverage reason the operator
// writes (internal/observer observeCallers and observeExternals, and
// not-evaluated from the controller's resetDerivedStatus) means for the person
// asked to approve. The slug stays in the message as the citation.
var coverageReasons = map[string]string{
	"shared-netns":             "a pod on the host network can call it and the node agent cannot tell which one",
	"stale-node":               "a node agent has not reported recently",
	"collection-failed":        "a node agent failed to collect connections",
	"degraded-node":            "a node agent is not fully running",
	"truncated":                "there were more entries than the operator keeps",
	"node-without-agent":       "a node runs pods but has no node agent",
	"unresolved-caller":        "a caller could not be matched to a pod and its owner",
	"agent-data-unreadable":    "the operator could not read the node agent reports",
	"no-agent-data":            "no node agent has reported",
	"service-unreadable":       "the operator could not read the Service",
	"pods-unreadable":          "the operator could not read the pods",
	"pactos-unreadable":        "the operator could not read the Pactos",
	"not-evaluated":            "the operator could not evaluate this Pacto's runtime evidence",
	"stale-status":             "the operator has not updated this status recently",
	"status-format":            "the operator that wrote this status does not report external systems",
	"pod-spec-unavailable":     "the operator could not read the workload's pod spec",
	"configmap-unreadable":     "the operator could not read a ConfigMap the pod spec references",
	"serviceentry-reader-off":  "the cluster serves no Istio ServiceEntries API, so the operator cannot read Istio ServiceEntries",
	"serviceentry-unreadable":  "the operator could not read the Istio ServiceEntries",
	"object-host-unclassified": "a Service or ServiceEntry names a host outside the cluster that gives no identity, such as one under a private suffix",
	"services-unreadable":      "the operator could not read the cluster's Services, so it cannot tell a call inside the cluster from one leaving it",
	"no-running-pods":          "the workload runs no pod the node agent can watch",
	"dns-unavailable":          "a node agent cannot read DNS answers, so it cannot name the systems the workload calls",
	"ambiguous-peers":          "an address the workload called answers for more than one domain, so the node agent cannot tell which one it called",
	"unnamed-public":           "the workload called a public address the node agent saw no DNS answer for",
	"unnamed-private-peers":    "the workload called a private address outside the cluster that the node agent cannot name",
	"gateway-log-format":       "the egress gateway's access log is in a format the operator cannot read",
	"gateway-access-log-off":   "the egress gateway writes no access log",
}

// InitClusterCommand is the command a hold prints to draft a contract for an
// uncontracted caller in the namespace: design section 3.2's syntax, which
// ladder-bulk implements.
func InitClusterCommand(namespace string) string {
	return "pacto init --cluster --namespace " + namespace + " --out <dir>"
}

// ObservedEdges are the runtime calls a Pacto's status shows: one edge per
// DEPENDENCY_UNDECLARED finding the node agent raised, from this service to
// the one it called, one per uncontracted caller of this service, under a
// k8s: name that never resolves to a contracted service, and one per
// observedEgress entry, from this service to the host it reached, which the
// fold classifies into an External. They carry no count
// or time, so the snapshot changes only when the set of calls does. p must
// carry status.contract.
func ObservedEdges(p PactoView) []fleet.ObservedEdge {
	svc := p.Status.Contract.ServiceName
	var out []fleet.ObservedEdge
	for _, f := range p.Status.Findings {
		if f.Code == "DEPENDENCY_UNDECLARED" && slices.ContainsFunc(f.EvidenceRefs, func(r EvidenceRefView) bool { return r.Collector == "ebpf" }) {
			out = append(out, fleet.ObservedEdge{From: svc, To: strings.TrimPrefix(f.Subject, "dependency/"), Count: 1})
		}
	}
	for _, c := range p.Status.ObservedCallers {
		out = append(out, fleet.ObservedEdge{From: CallerName(c.Namespace, c.Kind, c.Name), To: svc, Count: 1})
	}
	for _, e := range p.Status.ObservedEgress {
		out = append(out, fleet.ObservedEdge{From: svc, To: e.Host, Count: 1})
	}
	return out
}

// callersUnseen is why the runtime callers of the service cannot be ruled
// out, one line per target of it that says so, sorted.
func callersUnseen(snap *fleet.FleetSnapshot, service fleet.ServiceKey) []string {
	var out []string
	for _, t := range snap.Targets {
		for _, l := range t.Limitations {
			if t.ServiceKey == service && l.Code == LimitationCallersUnseen && !slices.Contains(out, l.Message) {
				out = append(out, l.Message)
			}
		}
	}
	slices.Sort(out)
	return out
}

// callerNamespace is the namespace in a CallerName.
func callerNamespace(caller string) string {
	_, ns, _, _, _ := fleet.ParseK8sCaller(caller)
	return ns
}

// CallerName is the identity of a workload with no contract. Delivery plans
// over one cluster, so it carries none (see [fleet.K8sCaller]).
func CallerName(namespace, kind, name string) string {
	return fleet.K8sCaller("", namespace, kind, name)
}

// revisionFromStatus rebuilds the declared part of a contract (service, owner,
// interfaces with their guarantees and dependencies with their ranges and
// requirements) from a Pacto's status. A status an older operator wrote
// records no guarantees or requirements, and the revision says so. Its identity is the evaluated
// contract digest when the operator recorded one, else a hash of the rebuilt
// contract, which is stable for as long as the status is.
func revisionFromStatus(p PactoView) (fleet.RawRevision, string) {
	c := p.Status.Contract
	ct := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: c.ServiceName, Version: c.Version}}
	if c.Owner != nil {
		ct.Service.Owner = *c.Owner
	}
	ct.Interfaces = p.Status.Interfaces
	for _, d := range p.Status.Dependencies {
		ct.Dependencies = append(ct.Dependencies, contract.Dependency{Name: d.Name, Ref: d.Ref, Required: d.Required, Compatibility: d.Compatibility, External: d.External, Version: d.Version, Requires: d.Requires})
	}
	digest := ""
	if e := p.Status.EvaluatedRevision; e != nil {
		digest = e.ContractDigest
	}
	if digest == "" {
		b, _ := json.Marshal(ct)
		digest = sha256Hex(b)
	}
	raw := fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: ct}, Domain: contract.OCIDomain(c.ResolvedRef),
		RequestedRef: c.ResolvedRef, ResolvedRef: c.ResolvedRef, Digest: digest,
	}
	if p.Status.StatusFormat < StatusFormatGuarantees {
		raw.GuaranteesUnseen = statusPredatesGuarantees(c.ServiceName)
	}
	return raw, digest
}

// statusPredatesGuarantees is why a status without statusFormat says nothing
// about guarantees: until the operator rewrites it, Pacto cannot see them.
func statusPredatesGuarantees(service string) string {
	return service + "'s status was written by an operator that does not record what it requires or guarantees, so Pacto cannot see it until the operator reconciles it again"
}

func parseTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// PactosFor returns the Pactos in namespace whose contract is service.
func PactosFor(pactos []PactoView, namespace, service string) []PactoView {
	return slices.DeleteFunc(slices.Clone(pactos), func(p PactoView) bool {
		return p.Metadata.Namespace != namespace || p.Status.Contract == nil || p.Status.Contract.ServiceName != service
	})
}

// EnvironmentLabel on a Pacto names the environment its workload runs in. It
// places the Pacto when the namespace it runs in does not.
const EnvironmentLabel = fleet.TargetEnvironmentLabel

// KustomizationView is the part of a Flux Kustomization the promoter reads to
// learn where a Stage's version runs: the namespace it applies into.
type KustomizationView struct {
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		TargetNamespace string `json:"targetNamespace"`
		SourceRef       struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"sourceRef"`
	} `json:"spec"`
}

// DecodeKustomizations decodes a JSON list of Kustomizations ({"items": [...]}).
func DecodeKustomizations(data []byte) ([]KustomizationView, error) {
	var list struct {
		Items []KustomizationView `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Misplaced is a Pacto whose environment label no Stage declares: Service is
// its contract's service ("" when it has none) and Note says why it counts.
type Misplaced struct {
	Service, Note string
}

// misplacedLabels returns the environments the Stages declare and every Pacto
// whose environment label is not one of them.
func misplacedLabels(pactos []PactoView, stages []StageObject) (known map[string]bool, misplaced []Misplaced) {
	known = map[string]bool{}
	for _, s := range stages {
		known[s.Spec.Environment] = true
	}
	for _, p := range pactos {
		if env := p.Metadata.Labels[EnvironmentLabel]; env != "" && !known[env] {
			m := Misplaced{Note: fmt.Sprintf("Pacto %s/%s is labelled for environment %q, which no Stage declares", p.Metadata.Namespace, p.Metadata.Name, env)}
			if p.Status.Contract != nil {
				m.Service = p.Status.Contract.ServiceName
			}
			misplaced = append(misplaced, m)
		}
	}
	return known, misplaced
}

// EnvironmentPactos keeps the Pactos that may run in environment. A provider
// version is eligible in an environment only if every consumer running there
// accepts it, so a consumer Pacto can place in another environment does not
// hold it.
//
// Pacto places a Pacto from where it runs, never from where a Stage object
// lives: by its EnvironmentLabel, else by its namespace when a Stage's version
// runs there (the targetNamespace of the Kustomization that applies the
// Stage's apply OCIRepository; an Argo CD Stage places only by label). A Pacto it cannot place counts in every
// environment, and with the Stages or the Kustomizations unreadable (placeErr)
// every Pacto counts: a consumer Pacto cannot place is one it cannot rule out.
// A label naming an environment no Stage declares places nothing either: that
// Pacto counts everywhere, and misplaced names it so the plan can say why it
// cannot see which consumers run here.
func EnvironmentPactos(pactos []PactoView, stages []StageObject, kustomizations []KustomizationView, placeErr error, environment string) ([]PactoView, []Misplaced) {
	if placeErr != nil {
		return pactos, nil
	}
	known, misplaced := misplacedLabels(pactos, stages)
	envs := map[string]map[string]bool{} // namespace -> the environments Stages apply into it
	for _, k := range kustomizations {
		src, target := k.Spec.SourceRef, k.Spec.TargetNamespace
		if src.Kind != "OCIRepository" || target == "" {
			continue
		}
		for _, s := range stages {
			if s.Spec.ApplyRef.Kind != ApplierArgoCD && s.Spec.ApplyRef.Name == src.Name && s.Metadata.Namespace == cmp.Or(src.Namespace, k.Metadata.Namespace) {
				if envs[target] == nil {
					envs[target] = map[string]bool{}
				}
				envs[target][s.Spec.Environment] = true
			}
		}
	}
	return slices.DeleteFunc(slices.Clone(pactos), func(p PactoView) bool {
		if env := p.Metadata.Labels[EnvironmentLabel]; env != "" {
			return known[env] && env != environment
		}
		placed := envs[p.Metadata.Namespace]
		return len(placed) > 0 && !placed[environment]
	}), misplaced
}
