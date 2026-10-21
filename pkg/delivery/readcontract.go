package delivery

import (
	"context"
	"encoding/json"
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
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
		Generation int64  `json:"generation"`
	} `json:"metadata"`
	Status PactoStatusView `json:"status"`
}

// PactoStatusView is the part of Pacto.status the promoter reads.
type PactoStatusView struct {
	ContractStatus     string            `json:"contractStatus"`
	ObservedGeneration int64             `json:"observedGeneration"`
	LastReconciledAt   string            `json:"lastReconciledAt"`
	Contract           *ContractInfoView `json:"contract"`
	Dependencies       []DependencyView  `json:"dependencies"`
	EvaluatedRevision  *EvaluatedView    `json:"evaluatedRevision"`
	Findings           []FindingView     `json:"findings"`
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
		})
	}
	src := fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col)
	return fleet.Build(ctx, fleet.BuildOptions{Now: func() time.Time { return now }}, src)
}

// revisionFromStatus rebuilds the declared part of a contract (service, owner
// and dependency ranges) from a Pacto's status. Its identity is the evaluated
// contract digest when the operator recorded one, else a hash of the rebuilt
// contract, which is stable for as long as the status is.
func revisionFromStatus(p PactoView) (fleet.RawRevision, string) {
	c := p.Status.Contract
	ct := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: c.ServiceName, Version: c.Version}}
	if c.Owner != nil {
		ct.Service.Owner = *c.Owner
	}
	for _, d := range p.Status.Dependencies {
		ct.Dependencies = append(ct.Dependencies, contract.Dependency(d))
	}
	digest := ""
	if e := p.Status.EvaluatedRevision; e != nil {
		digest = e.ContractDigest
	}
	if digest == "" {
		b, _ := json.Marshal(ct)
		digest = sha256Hex(b)
	}
	return fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: ct}, Domain: contract.OCIDomain(c.ResolvedRef),
		RequestedRef: c.ResolvedRef, ResolvedRef: c.ResolvedRef, Digest: digest,
	}, digest
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

// EnvironmentPactos keeps the Pactos running in environment, the one the
// Stage in namespace delivers to. A provider version is eligible in an
// environment only if every consumer running there accepts it, so a consumer
// running only in another environment does not hold it.
//
// A Pacto runs in an environment when it lives in the Stage's own namespace or
// in a namespace that holds a Stage of that environment. One in a namespace no
// Stage lives in cannot be placed, so it counts in every environment, and with
// the Stages unreadable (stagesErr) every Pacto counts.
func EnvironmentPactos(pactos []PactoView, stages []StageObject, stagesErr error, namespace, environment string) []PactoView {
	if stagesErr != nil {
		return pactos
	}
	envs := map[string]map[string]bool{}
	for _, s := range stages {
		if envs[s.Metadata.Namespace] == nil {
			envs[s.Metadata.Namespace] = map[string]bool{}
		}
		envs[s.Metadata.Namespace][s.Spec.Environment] = true
	}
	return slices.DeleteFunc(slices.Clone(pactos), func(p PactoView) bool {
		ns := p.Metadata.Namespace
		return ns != namespace && len(envs[ns]) > 0 && !envs[ns][environment]
	})
}
