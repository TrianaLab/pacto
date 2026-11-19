package scenario

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The target snapshot the demo publishes: what the demo cluster runs, signed
// and pushed to <registry>/pacto-targets, so `pacto impact` against the demo
// registry reads its targets with no kubeconfig, as CI reads a real cluster's.
const (
	// ComposeTargetsCluster is the demo cluster's UID, the snapshot's tag.
	ComposeTargetsCluster = "compose-demo-0000-0000-0000-000000000001"
	// ComposeTargetSnapshotFile is the snapshot document inside the artifact.
	ComposeTargetSnapshotFile = ComposeArtifactMount + "/targets/snapshot.json"
	// composeTargetsNamespace is the namespace the demo's Pactos are placed in.
	composeTargetsNamespace = "demo"
	composeTargetsKeyID     = "compose-targets"
	// composeTargetsDir holds the snapshot key in the state volume: the image
	// HOME is the one path the non-root user can write a named volume at.
	composeTargetsDir = composeStateMount + "/targets"
	composeTargetsKey = composeTargetsDir + "/" + composeTargetsKeyID + ".key"
	// composeTargetsStamped is the document as published, its reconcile time now.
	composeTargetsStamped = "/tmp/snapshot.json"
)

// composeReconciledPlaceholder is the lastReconciledAt the document carries,
// which the publish loop replaces with the time of each publish.
var composeReconciledPlaceholder = story.Now.UTC().Format(time.RFC3339)

// composeTargets is the service that mints the snapshot key on first start and
// republishes the snapshot every TargetSnapshotInterval. A failed publish is
// retried in seconds, so the first one lands as soon as the registry answers.
func composeTargets(image string, input composeInput) composeService {
	// Each publish stamps the Pactos' lastReconciledAt with now, as a running
	// operator would, so their targets read current. $$ survives interpolation.
	stamp := "sed \"s/" + composeReconciledPlaceholder + "/$$(date -u +%Y-%m-%dT%H:%M:%SZ)/g\" " + ComposeTargetSnapshotFile + " >" + composeTargetsStamped
	publish := "pacto delivery targets publish " + composeTargetsStamped + " --registry " + ComposeDomain + " --key " + composeTargetsKey
	script := strings.Join([]string{
		"mkdir -p " + composeTargetsDir,
		"[ -f " + composeTargetsKey + " ] || pacto evidence keygen --out " + composeTargetsDir + " --key-id " + composeTargetsKeyID,
		"while :; do",
		"  " + stamp,
		"  if " + publish + "; then sleep " + fmt.Sprint(int(delivery.TargetSnapshotInterval/time.Second)) + "; else sleep 3; fi",
		"done",
	}, "\n") + "\n"
	return composeService{
		Image:       image,
		Restart:     "unless-stopped",
		Entrypoint:  []string{"/bin/sh", "-euc"},
		Command:     []string{script},
		Environment: map[string]string{"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost},
		// The registry has no healthcheck; the publish loop retries until it answers.
		DependsOn: map[string]composeDep{"registry": {Condition: "service_started"}},
		Volumes:   []string{composeStateVolume + ":" + composeStateMount},
		Configs:   mounts([]composeInput{input}),
		// Healthy once a publish wrote the public key a reader trusts.
		Healthcheck: &composeHealth{
			Test:        []string{"CMD-SHELL", "test -f " + composeTargetsKey + ".pub.pem"},
			Interval:    "3s",
			Timeout:     "3s",
			Retries:     40,
			StartPeriod: "60s",
		},
	}
}

// composeTargetSnapshot is the snapshot document: one PactoView per service
// the scenario places, its status contract built from the deployed bundle as
// the operator would write it. asOf and publisher are placeholders the
// publish replaces.
func (s Scenario) composeTargetSnapshot(digests map[string]string) (composeInput, error) {
	files, err := s.MaterializeFiles(ComposeDomain)
	if err != nil {
		return composeInput{}, err
	}
	snap := delivery.TargetSnapshot{
		Cluster: ComposeTargetsCluster, AsOf: story.Now.UTC().Format(time.RFC3339),
		Completeness: fleet.CompletenessComplete, Publisher: "compose:targets",
		Pactos: []delivery.PactoView{}, Workloads: []fleet.RawInferredService{},
	}
	for _, svc := range s.Services {
		if svc.Workload == nil {
			continue
		}
		p, err := s.composePactoView(svc, files, digests)
		if err != nil {
			return composeInput{}, err
		}
		snap.Pactos = append(snap.Pactos, p)
	}
	if err := snap.Check(); err != nil {
		return composeInput{}, fmt.Errorf("scenario %s: target snapshot: %w", s.Name, err)
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return composeInput{}, err
	}
	return composeInput{name: "targets_snapshot.json", target: ComposeTargetSnapshotFile, content: literal(string(body) + "\n")}, nil
}

// composePactoView is the Pacto of one placed service, as the read contract.
func (s Scenario) composePactoView(svc Service, files, digests map[string]string) (delivery.PactoView, error) {
	rev, err := svc.DeployedRevision()
	if err != nil {
		return delivery.PactoView{}, fmt.Errorf("scenario %s: %w", s.Name, err)
	}
	ref, err := s.digestRef(svc, rev, ComposeDomain, digests)
	if err != nil {
		return delivery.PactoView{}, err
	}
	ct, err := contract.Parse(strings.NewReader(files[path.Join(rev.Dir, "pacto.yaml")]))
	if err != nil {
		return delivery.PactoView{}, fmt.Errorf("scenario %s: %s: %w", s.Name, rev.Dir, err)
	}
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = composeTargetsNamespace, svc.Name
	p.Spec.Target = &delivery.PactoTargetView{WorkloadRef: &delivery.WorkloadRefView{Kind: "Deployment", Name: svc.Workload.Name}}
	if svc.Workload.Port != 0 {
		p.Spec.Target.ServiceName = svc.Workload.Name
	}
	info := &delivery.ContractInfoView{ServiceName: ct.Service.Name, Version: ct.Service.Version, ResolvedRef: ref}
	if o := ct.Service.Owner; o.Team != "" || o.DRI != "" || len(o.Contacts) > 0 {
		info.Owner = &o
	}
	p.Status = delivery.PactoStatusView{StatusFormat: delivery.StatusFormatGuarantees, Contract: info,
		LastReconciledAt: composeReconciledPlaceholder, Interfaces: ct.Interfaces}
	for _, d := range ct.Dependencies {
		p.Status.Dependencies = append(p.Status.Dependencies, delivery.DependencyView{Name: d.Name, Ref: d.Ref, Required: d.Required, Compatibility: d.Compatibility})
	}
	return p, nil
}
