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
	Status PactoStatusView `json:"status"`
}

// StatusFormatGuarantees is the first Pacto.status.statusFormat: the status
// records interface guarantees and dependency requirements. A status without
// statusFormat was written by an older operator and records neither, so what
// it would say about them is unknown.
const StatusFormatGuarantees = 1

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

// Snapshot builds the operational graph plan() projects a change onto: one
// revision and one target per reconciled Pacto (the revision rebuilt from the
// status the operator already wrote, so no registry read is needed per
// consumer), plus the revisions on both sides of the change.
//
// A Pacto the operator has not reconciled yet carries no contract in its status.
// It is reported as a limitation, which makes the snapshot partial: a consumer
// Pacto cannot read is a consumer it cannot rule out.
//
// listErr is the error listing the Pactos, if any: Pacto cannot rule out a
// consumer it could not list, so it makes the snapshot partial too.
func Snapshot(ctx context.Context, pactos []PactoView, listErr error, sides []Revision, now time.Time) (*fleet.FleetSnapshot, error) {
	col := &fleet.Collection{}
	if listErr != nil {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: "PACTOS_UNREADABLE", Source: SnapshotSourceID, Message: "the Pacto objects could not be listed: " + listErr.Error(),
		})
	}
	for _, side := range sides {
		if side.Bundle == nil {
			continue
		}
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		col.Revisions = append(col.Revisions, fleet.RawRevision{
			Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef),
			RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + digest, Digest: digest,
		})
	}
	for _, p := range pactos {
		c := p.Status.Contract
		if c == nil || c.ServiceName == "" {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: "PACTO_NOT_RECONCILED", Source: SnapshotSourceID,
				Message: "Pacto " + p.Metadata.Namespace + "/" + p.Metadata.Name + " has no reconciled contract yet",
			})
			continue
		}
		rev, digest := revisionFromStatus(p)
		col.Revisions = append(col.Revisions, rev)
		reconciled := parseTime(p.Status.LastReconciledAt)
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope: p.Metadata.Namespace, Kind: fleet.SourceKindKubernetes, Name: p.Metadata.Name,
			Service: c.ServiceName, Domain: rev.Domain, ResolvedRef: c.ResolvedRef, Digest: digest,
			Compliance: p.Status.ContractStatus, EvidenceAt: reconciled, ReconciledAt: reconciled,
			Limitations: callerLimitations(p, reconciled, now),
		})
		col.Observed = append(col.Observed, observedEdges(p)...)
	}
	src := fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col)
	return fleet.Build(ctx, fleet.BuildOptions{Now: func() time.Time { return now }}, src)
}

// LimitationCallersUnseen marks a target whose runtime callers Pacto cannot
// rule out: its caller coverage is not complete, or its status stopped
// updating. It stays on the target, so the rest of the snapshot stays complete.
const LimitationCallersUnseen = "OBSERVED_CALLERS_UNSEEN"

// callerLimitations says when the Pacto's runtime callers cannot be ruled out.
// A coverage the operator has not rewritten within twice its refresh interval
// is stale whatever it says: an operator that stopped would otherwise leave a
// complete coverage in place forever. No coverage means runtime observation is
// not configured, which changes nothing.
func callerLimitations(p PactoView, reconciled *time.Time, now time.Time) []fleet.Limitation {
	cov := p.Status.ObservedCallersCoverage
	if cov == nil {
		return nil
	}
	state, reason := cov.State, cov.Reason
	if reconciled == nil || cov.RefreshSeconds <= 0 || now.Sub(*reconciled) > 2*time.Duration(cov.RefreshSeconds)*time.Second {
		state, reason = "unavailable", "stale-status"
	}
	if state == "complete" {
		return nil
	}
	return []fleet.Limitation{{Code: LimitationCallersUnseen, Source: SnapshotSourceID,
		Message: "runtime callers of " + p.Status.Contract.ServiceName + " cannot be ruled out: " +
			cmp.Or(coverageReasons[reason], "the operator reported "+reason) + " (coverage: " + state + ", " + reason + ")"}}
}

// coverageReasons says in plain words what each caller-coverage reason the
// operator writes (internal/observer observeCallers, and not-evaluated from
// the controller's resetDerivedStatus) means for the person
// asked to approve. The slug stays in the message as the citation.
var coverageReasons = map[string]string{
	"shared-netns":          "a pod on the host network can call it and the node agent cannot tell which one",
	"stale-node":            "a node agent has not reported recently",
	"collection-failed":     "a node agent failed to collect connections",
	"degraded-node":         "a node agent is not fully running",
	"truncated":             "there were more connections or callers than the operator keeps",
	"node-without-agent":    "a node runs pods but has no node agent",
	"unresolved-caller":     "a caller could not be matched to a pod and its owner",
	"agent-data-unreadable": "the operator could not read the node agent reports",
	"no-agent-data":         "no node agent has reported",
	"service-unreadable":    "the operator could not read the Service",
	"pods-unreadable":       "the operator could not read the pods",
	"pactos-unreadable":     "the operator could not read the Pactos",
	"not-evaluated":         "the operator could not evaluate this Pacto's runtime evidence",
	"stale-status":          "the operator has not updated this status recently",
}

// InitClusterCommand is the command a hold prints to draft a contract for an
// uncontracted caller in the namespace: design section 3.2's syntax, which
// ladder-bulk implements.
func InitClusterCommand(namespace string) string {
	return "pacto init --cluster --namespace " + namespace + " --out <dir>"
}

// observedEdges are the runtime calls a Pacto's status shows: one edge per
// DEPENDENCY_UNDECLARED finding the node agent raised, from this service to
// the one it called, and one per uncontracted caller of this service, under a
// k8s: name that never resolves to a contracted service. They carry no count
// or time, so the snapshot changes only when the set of calls does.
func observedEdges(p PactoView) []fleet.ObservedEdge {
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
	ns, _, _ := strings.Cut(strings.TrimPrefix(caller, "k8s:"), "/")
	return ns
}

// CallerName is the identity of a workload with no contract.
func CallerName(namespace, kind, name string) string {
	return "k8s:" + namespace + "/" + kind + "/" + name
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
const EnvironmentLabel = "delivery.pacto.trianalab.io/environment"

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
