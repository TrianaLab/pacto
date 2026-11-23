package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// A producer outside Pacto, compose-idp, contributes one service through the
// open seams: the seed pushes its signed record to
// <registry>/pacto-inferred/compose-idp/services and sends its signed report
// of a target of one Evidence Server subject, both with the key the Evidence
// Server mints for it and grants its own contribution path.
const (
	// ComposeContributor is the producer, and its key id.
	ComposeContributor = "compose-idp"
	// ComposeContributedService is the service its record describes.
	ComposeContributedService = "ledger-sync"
	// composeContributedTargetSuffix names the target it reports after the
	// subject's service: <service>-idp.
	composeContributedTargetSuffix = "-idp"
	// ComposeContributionDir holds the record's bundle and the report.
	ComposeContributionDir = ComposeArtifactMount + "/contribution"
	// composeContributionExport is the export the record cites.
	composeContributionExport = "export.json"
)

// composeContributorSigner is the producer's identity in the trust config.
var composeContributorSigner = Signer{Producer: ComposeContributor, KeyID: ComposeContributor}

// composeContributionStamp is the time the report carries, which the seed
// replaces with the time it sends it, so the target reads current.
var composeContributionStamp = story.Now.UTC().Format(time.RFC3339)

// composeContribution is the producer's inputs, as seed config entries: the
// record bundle (a name and a version in pacto.yaml, the dependency on the
// subject's service in pacto.inferred.yaml) and the WorkloadObserved report
// on that subject, which it returns. subjects are the Evidence Server's
// subjects.
func (s Scenario) composeContribution(subjects []string) (inputs []composeInput, subject string, err error) {
	ref, calls, err := s.composeContributionSubject(subjects)
	if err != nil {
		return nil, "", err
	}
	if _, taken := s.Service(ComposeContributedService); taken {
		return nil, "", fmt.Errorf("scenario %s: %s has a contract, so its contribution would be superseded", s.Name, ComposeContributedService)
	}
	export := `{"` + ComposeContributedService + `":["` + calls + `"]}` + "\n"
	sum := sha256.Sum256([]byte(export))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: composeContributionExport, Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[" + calls + "]", Value: calls, Source: composeContributionExport + ":1", Confidence: contract.ConfidenceLikely}}}

	at, _ := time.Parse(time.RFC3339, composeContributionStamp)
	target := evidence.SubjectRef{Kind: "service", Name: calls + composeContributedTargetSuffix}
	set := evidence.EvidenceSet{Subject: target, ContractRef: ref, Source: ComposeContributor, ObservedAt: at,
		Observations: []evidence.Observation{evidence.NewWorkloadObserved(target, "service", evidence.Provenance{Collector: ComposeContributor, DetectedAt: at})}}
	report, _ := json.MarshalIndent(set, "", "  ") // a set the scenario built always marshals
	files := map[string]string{
		"bundle/pacto.yaml":                     "pactoVersion: \"2.5\"\nservice:\n  name: " + ComposeContributedService + "\n  version: 0.1.0\n",
		"bundle/" + composeContributionExport:   export,
		"bundle/" + contract.InferredRecordFile: string(rec.Marshal()),
		"workload.json":                         string(report) + "\n",
	}
	for _, rel := range sortedKeys(files) {
		inputs = append(inputs, composeInput{name: "contribution_" + strings.ReplaceAll(rel, "/", "_"),
			target: ComposeContributionDir + "/" + rel, content: literal(files[rel])})
	}
	return inputs, ref, nil
}

// composeContributionSubject is the subject the producer reports on and the
// name of its service: a deaf workload's revision when there is one, which
// no other check counts the evidence targets of, else the first evidence's.
func (s Scenario) composeContributionSubject(subjects []string) (ref, service string, err error) {
	var svc Service
	if deaf := s.DeafServices(); len(deaf) > 0 {
		svc = deaf[0]
	} else if len(s.Evidence) > 0 {
		svc, _ = s.Service(s.Evidence[0].Service)
	}
	for _, ref := range subjects {
		if repo, _, _ := strings.Cut(contract.TrimOCIScheme(ref), "@"); svc.Repo != "" && repo == ComposeDomain+"/"+svc.Repo {
			return ref, svc.Name, nil
		}
	}
	return "", "", fmt.Errorf("scenario %s: a contribution reports on an evidence subject, and none is published for %q", s.Name, svc.Name)
}

// composeContributionGrant is the producer's trust config entry: its own
// contribution path, and the repository of the subject ref it reports on.
func composeContributionGrant(ref string) []string {
	repo, _, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	return []string{ComposeDomain + "/" + contract.InferredRepository + "/" + ComposeContributor, repo}
}
