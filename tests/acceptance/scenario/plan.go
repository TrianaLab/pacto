package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// This file is the EXECUTION projection: everything the Kind harness needs to
// bring the fixture up, as data it reads rather than as literals it restates.
//
// Materialize and TraceExport already stopped the harness from re-declaring
// bundle CONTENT, but the harness still knew, on its own, which directory went to
// which repository under which tag, which service ran which revision, what the
// observation source was called, and who signed which evidence about what. Change
// a Repo or flip which Revision is Deployed and the shell went on describing the
// old fixture — the exact failure class the scenario package exists to remove,
// just one level up from the bundles.
//
// Two phases, because a digest does not exist until the push has happened:
//
//	Plan              before the push: what to publish, run, mount and sign
//	PactoCRs          after it: the cluster projection, pinned to real digests
//	EvidencePayloads  after it: each envelope's EvidenceSet, pinned the same way
//
// The interface to the shell is tab-delimited records, not generated shell: the
// harness reads the plan as DATA and never sources or evaluates it. Every field
// is validated on the way out — non-empty, and free of the tab, newline and
// carriage return that are the only characters able to forge a record — and the
// harness re-checks each record's arity where it consumes it.

// Plan record kinds. Each is a line: the kind, then its fields, TAB-separated.
//
//	push         <digest key> <bundle dir> <repository:tag>
//	observation  <source id> <configMap name> <file key> <export path>
//	workload     <service> <deployment> <service port, 0 when unexposed>
//	signer       <producer id> <key id>
//	evidence     <subject> <payload path> <sequence> <envelope id>
const (
	RecordPush        = "push"
	RecordObservation = "observation"
	RecordWorkload    = "workload"
	RecordSigner      = "signer"
	RecordEvidence    = "evidence"
)

// observationFileKey is the key an observation ConfigMap stores its export under,
// and the file name the operator mounts it as. One value, two consumers: the
// ConfigMap the harness creates and the Helm value it sets.
const observationFileKey = "traces.json"

// checkIntervalSeconds is how often the operator re-checks a projected CR. A
// timing knob of the run rather than an identity of the fixture, so it lives with
// the projection and not in the scenario.
const checkIntervalSeconds = 30

// DigestKey names one published revision, in the plan and in the digest map the
// harness hands back once the registry has assigned one. Service and version,
// because one artifact is published per version of a service.
func DigestKey(service, version string) string { return service + "@" + version }

// ObservationConfigMap is the ConfigMap an observation source's export is carried
// in. Derived from the source id so the ConfigMap the harness creates and the
// Helm value naming it cannot drift.
func ObservationConfigMap(sourceID string) string { return "pacto-" + sourceID }

// EnvelopeID is the id of the nth declared envelope (0-based). Producer-scoped
// sequence numbers are 1-based and monotonic, so the two are derived together.
func EnvelopeID(scenarioName string, i int) string {
	return scenarioName + "-ev-" + strconv.Itoa(i+1)
}

// evidencePayloadPath is where the EvidenceSet for the nth envelope is written.
// Plan names it before it exists so the harness never constructs a path.
func evidencePayloadPath(dir, scenarioName string, i int) string {
	return filepath.Join(dir, EnvelopeID(scenarioName, i)+".evidence.json")
}

// Plan renders the execution plan for a fixture materialized into dir.
func (s Scenario) Plan(dir string) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("scenario %s: no output directory to plan against", s.Name)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var b planBuilder
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			b.add(RecordPush, DigestKey(svc.Name, rev.Version), filepath.Join(dir, rev.Dir), svc.Repo+":"+rev.Version)
		}
		if svc.Workload == nil {
			continue
		}
		// The plan says WHAT runs, never which revision it runs: that is the CR's
		// business, after the push. So a workload record is identical whichever
		// revision the fixture deploys, and moving the deployment cannot reach the
		// shell at all.
		b.add(RecordWorkload, svc.Name, svc.Workload.Name, strconv.Itoa(svc.Workload.Port))
	}
	for _, src := range s.Sources {
		if src.Kind != SourceObservation {
			continue
		}
		b.add(RecordObservation, src.ID, ObservationConfigMap(src.ID), observationFileKey,
			filepath.Join(dir, src.ID+".json"))
	}
	signer, err := s.signer()
	if err != nil {
		return nil, err
	}
	if signer != (Signer{}) {
		b.add(RecordSigner, signer.Producer, signer.KeyID)
	}
	for i, ev := range s.Evidence {
		b.add(RecordEvidence, ev.Service, evidencePayloadPath(dir, s.Name, i),
			strconv.Itoa(i+1), EnvelopeID(s.Name, i))
	}
	return b.bytes(s.Name)
}

// signer is the ONE identity every declared envelope is signed by.
//
// The trust store the harness installs is a single Secret holding a single public
// key, so a second producer would need a second keypair and a Secret that merges
// them. No fixture has needed that, and the alternative to refusing it here is a
// harness that silently signs everything with whichever signer it read first —
// which is the class of quiet disagreement this file exists to end.
//
// ponytail: one signer per fixture. Merge keys in trust_keypair if a fixture ever
// has to prove cross-producer ingestion.
func (s Scenario) signer() (Signer, error) {
	var out Signer
	for _, ev := range s.Evidence {
		switch {
		case ev.Signer.Producer == "" || ev.Signer.KeyID == "":
			return Signer{}, fmt.Errorf("scenario %s: evidence about %s declares no signer, so nothing could sign its envelope",
				s.Name, ev.Service)
		case out == (Signer{}):
			out = ev.Signer
		case out != ev.Signer:
			return Signer{}, fmt.Errorf("scenario %s: evidence about %s is signed by %s/%s but %s/%s signs the rest; the fixture installs one trust key",
				s.Name, ev.Service, ev.Signer.Producer, ev.Signer.KeyID, out.Producer, out.KeyID)
		}
	}
	return out, nil
}

// planBuilder accumulates records and the first field that could not be written.
type planBuilder struct {
	lines []string
	err   error
}

func (b *planBuilder) add(kind string, fields ...string) {
	for _, f := range fields {
		if f == "" {
			b.note(fmt.Errorf("%s record has an empty field", kind))
			return
		}
		// The delimiter set, and nothing else, can forge a record: a tab splits one
		// field into two, either newline ends the record early.
		if strings.ContainsAny(f, "\t\n\r") {
			b.note(fmt.Errorf("%s record field %q contains a delimiter", kind, f))
			return
		}
	}
	b.lines = append(b.lines, kind+"\t"+strings.Join(fields, "\t"))
}

func (b *planBuilder) note(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *planBuilder) bytes(scenarioName string) ([]byte, error) {
	if b.err != nil {
		return nil, fmt.Errorf("scenario %s: %w", scenarioName, b.err)
	}
	return []byte(strings.Join(b.lines, "\n") + "\n"), nil
}

// PactoCRs renders the Pacto custom resources for every service that runs
// something: the cluster projection of the fixture's operational targets.
//
// It runs after the push because a CR pins the IMMUTABLE digest the registry
// assigned, which is what makes the operator publish a real resolved contract
// identity instead of re-resolving a tag. Which revision a CR pins comes from
// Deployed, so moving the flag moves the applied CR — the shell has no say.
func (s Scenario) PactoCRs(namespace, domain string, digests map[string]string) ([]byte, error) {
	if namespace == "" || domain == "" {
		return nil, fmt.Errorf("scenario %s: a Pacto CR needs both a namespace and an OCI domain", s.Name)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, svc := range s.Services {
		if svc.Workload == nil {
			continue
		}
		rev, err := svc.DeployedRevision()
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", s.Name, err)
		}
		ref, err := s.digestRef(svc, rev, domain, digests)
		if err != nil {
			return nil, err
		}
		cr := pactoCR{
			APIVersion: "pacto.trianalab.io/v1alpha1",
			Kind:       "Pacto",
			Metadata:   crMeta{Name: svc.Name, Namespace: namespace},
			Spec: crSpec{
				CheckIntervalSeconds: checkIntervalSeconds,
				ContractRef:          crContractRef{OCI: ref},
				Target: crTarget{
					WorkloadRef: crWorkloadRef{Name: svc.Workload.Name, Kind: "Deployment"},
				},
			},
		}
		if svc.Workload.Port != 0 {
			cr.Spec.Target.ServiceName = svc.Workload.Name
			cr.Spec.Target.InterfaceBindings = []crInterfaceBinding{
				{Interface: svc.Workload.Interface, ServicePort: svc.Workload.Port},
			}
		}
		if err := enc.Encode(cr); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("scenario %s: no service runs anything, so there is no target to declare", s.Name)
	}
	return buf.Bytes(), nil
}

// The CR shape, local to this projection. The real types live in the Kubernetes
// integration module, which this module must not import — and marshalling through
// a struct is what keeps every value escaped rather than interpolated.
type pactoCR struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   crMeta `yaml:"metadata"`
	Spec       crSpec `yaml:"spec"`
}

type crMeta struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type crSpec struct {
	CheckIntervalSeconds int           `yaml:"checkIntervalSeconds"`
	ContractRef          crContractRef `yaml:"contractRef"`
	Target               crTarget      `yaml:"target"`
}

type crContractRef struct {
	OCI string `yaml:"oci"`
}

type crTarget struct {
	ServiceName       string               `yaml:"serviceName,omitempty"`
	WorkloadRef       crWorkloadRef        `yaml:"workloadRef"`
	InterfaceBindings []crInterfaceBinding `yaml:"interfaceBindings,omitempty"`
}

type crWorkloadRef struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

type crInterfaceBinding struct {
	Interface   string `yaml:"interface"`
	ServicePort int    `yaml:"servicePort"`
}

// EvidencePayloads renders the EvidenceSet each declared envelope carries, keyed
// by the SAME path Plan already told the harness to expect it at — one derivation,
// so the writer and the reader cannot disagree about where a payload landed.
//
// The ContractRef resolves to the subject's REAL published bundle, so the target
// the Evidence Server produces points at content that exists in the registry
// rather than at a plausible-looking string.
func (s Scenario) EvidencePayloads(dir, domain string, digests map[string]string) (map[string][]byte, error) {
	if dir == "" || domain == "" {
		return nil, fmt.Errorf("scenario %s: an evidence payload needs both an output directory and an OCI domain", s.Name)
	}
	out := map[string][]byte{}
	for i, ev := range s.Evidence {
		set, err := s.evidenceSet(ev, domain, digests)
		if err != nil {
			return nil, err
		}
		body, err := json.MarshalIndent(set, "", "  ")
		if err != nil {
			return nil, err
		}
		out[evidencePayloadPath(dir, s.Name, i)] = append(body, '\n')
	}
	return out, nil
}

func (s Scenario) evidenceSet(ev Evidence, domain string, digests map[string]string) (evidence.EvidenceSet, error) {
	svc, ok := s.Service(ev.Service)
	if !ok {
		return evidence.EvidenceSet{}, fmt.Errorf("scenario %s: evidence names service %q, which the fixture never declares", s.Name, ev.Service)
	}
	if ev.Source == "" {
		return evidence.EvidenceSet{}, fmt.Errorf("scenario %s: evidence about %s names no source environment", s.Name, ev.Service)
	}
	observedAt, err := time.Parse(time.RFC3339, ev.ObservedAt)
	if err != nil {
		return evidence.EvidenceSet{}, fmt.Errorf("scenario %s: evidence about %s has an unparseable ObservedAt: %w", s.Name, ev.Service, err)
	}
	if len(svc.Revisions) == 0 {
		return evidence.EvidenceSet{}, fmt.Errorf("scenario %s: evidence about %s has no published revision to point at", s.Name, ev.Service)
	}
	rev := svc.Revisions[0]
	ref, err := s.digestRef(svc, rev, domain, digests)
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	workload, err := workloadType(rev, domain)
	if err != nil {
		return evidence.EvidenceSet{}, fmt.Errorf("scenario %s: evidence about %s: %w", s.Name, ev.Service, err)
	}
	value, err := json.Marshal(evidence.WorkloadObservation{Type: workload})
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	subject := evidence.SubjectRef{Kind: "service", Name: ev.Service}
	return evidence.EvidenceSet{
		Subject:     subject,
		ContractRef: "oci://" + ref,
		Source:      ev.Source,
		ObservedAt:  observedAt,
		Observations: []evidence.Observation{{
			Kind:       evidence.WorkloadObserved,
			Subject:    subject,
			Outcome:    evidence.Observed,
			Value:      value,
			Provenance: evidence.Provenance{Collector: ev.Source, DetectedAt: observedAt},
		}},
	}, nil
}

// workloadType reads the observed workload type out of the subject's OWN
// contract. An envelope claiming "service" about a bundle that declares a job
// would be the fixture contradicting itself, which is exactly the drift these
// projections exist to make impossible.
func workloadType(rev Revision, domain string) (string, error) {
	body, err := render(rev.Files["pacto.yaml"], domain)
	if err != nil {
		return "", err
	}
	c, err := contract.Parse(strings.NewReader(body))
	if err != nil {
		return "", err
	}
	if c.Workload == "" {
		return "", fmt.Errorf("%s declares no workload, so there is nothing to report as observed", rev.Dir)
	}
	return c.Workload, nil
}

// digestRef is the immutable reference to one published revision: the domain the
// harness brought up, the repository the scenario declares, and the digest the
// registry assigned to that exact bundle.
func (s Scenario) digestRef(svc Service, rev Revision, domain string, digests map[string]string) (string, error) {
	key := DigestKey(svc.Name, rev.Version)
	digest, ok := digests[key]
	if !ok || digest == "" {
		return "", fmt.Errorf("scenario %s: no digest was published for %s; the push must run before the cluster projection", s.Name, key)
	}
	return domain + "/" + svc.Repo + "@" + digest, nil
}
