// Package scenario is the canonical declarative description of a Pacto
// acceptance fixture.
//
// ONE scenario, several surfaces. The operational-graph vertical is described by
// a shell harness that publishes contract bundles and mounts an observation
// export, by a Go gate that proves the resulting Product facts, and by a browser
// suite that drives the same entities. Before this package the scenario was
// declared three times over — heredocs in the harness, flags on the gate, an
// ad-hoc handoff to the browser — and the copies could disagree silently: a
// version bumped in a heredoc surfaced only as the gate timing out on a revision
// nobody had published under that name.
//
// The scenario is DATA here, and each surface is a projection of it:
//
//	Materialize       the contract bundle directories the harness publishes
//	TraceExport       the OTLP export the operator mounts as an observation source
//	Plan              the machine-readable execution plan both harnesses consume
//	PactoCRs          the cluster projection of the deployed targets, once digests exist
//	EvidencePayloads  the EvidenceSet each declared envelope carries
//	HelmValues        the chart values that configure the Kubernetes surface
//	Compose           the Docker Compose surface, distributed as an OCI artifact
//	FactCount         how many Product facts this scenario obliges the gate to prove
//
// The expected Product facts are not a separate document: they ARE the scenario.
// A declared revision must be one canonical retrievable revision, a deployed one
// must have exactly one operational target linking to it, a relationship with an
// ObservedBy must be declared, observed and reconciled. The gate walks the value
// below; nothing about the fixture is written down twice.
//
// Journey inputs are the one thing that cannot be declared: a ServiceKey is
// domain-escaped and a RevisionKey carries a content id, so the browser suite is
// handed the keys the gate DISCOVERED, not keys anybody constructed. Journey
// names which service plays which part, so that handoff is still a projection of
// this value rather than a fourth declaration of the fixture.
//
// Bundle CONTENT stays literal. A fixture contract is meant to be read, and a
// generator for it would be a second, untested implementation of the contract
// schema. The declared identity beside it is proved to agree with the literal by
// this package's tests instead.
//
// Only projections with a consumer today exist. HelmValues projects the chart
// values that come from the SCENARIO and nothing else: the operator image, the
// insecure registry and the enabled components stay in the harness, because they
// are properties of the run and have no counterpart on the other surface.
//
// TWO surfaces now, which is what Surface is for. Kubernetes and Compose owe
// different facts, and the difference is DECLARED as a capability Compose does
// not provide rather than discovered as a shorter run: nothing there reconciles a
// Pacto CR, so no operational target is expected, and the gate subtracts exactly
// those facts and says which capability it skipped. Every other fact is owed on
// both, and parity_test.go compares the rendered projections to prove it.
//
// Values that cannot exist before execution stay runtime inputs, passed in: the
// registry address the harness happened to bring up, the digests the registry
// assigned, forwarded ports and temporary directories. Everything a projection
// needs beyond those is in this value.
package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// Scenario is a fixture the acceptance surfaces share.
type Scenario struct {
	// Name identifies the scenario in diagnostics.
	Name string
	// Services are every service the fixture publishes, in a stable order.
	Services []Service
	// Sources are the Data Sources the Product must publish for this fixture.
	Sources []Source
	// Relationships are the edges between services, declared and/or observed.
	Relationships []Relationship
	// Evidence is what arrives from outside the cluster, over the Evidence Server.
	Evidence []Evidence
	// Journey names the parts the browser suite drives.
	Journey Journey
}

// Service is one service the fixture publishes contract revisions for.
type Service struct {
	Name string
	// Repo is the repository path under the fixture's OCI domain.
	Repo string
	// EvidenceOnly marks a service whose bundle is published so a signed
	// EvidenceEnvelope can point at real content, but which the Product sees only
	// through the Evidence Server: it carries no OCI revisions in the fleet and
	// lands in whatever domain the evidence names, not the fixture's.
	EvidenceOnly bool
	// Workload is what the deployed revision runs as in the cluster. Nil means the
	// service publishes revisions but nothing runs them, so no Deployment and no
	// Pacto CR are projected for it.
	Workload  *Workload
	Revisions []Revision
}

// Workload is the cluster side of a deployed service: the Deployment the harness
// creates and the target the Pacto CR points at. It is declared here so the CR is
// a projection of this value rather than a YAML heredoc the shell maintains in
// parallel with it.
type Workload struct {
	// Name is the Deployment, and the CR's target workloadRef name.
	Name string
	// Interface and Port bind one contract interface to the port that serves it.
	// A zero Port means the workload is deliberately unexposed: no Service, no
	// binding, and the operator's only honest answer about interface availability
	// is Unknown.
	//
	// ponytail: one binding per workload. A second would be a slice; no fixture
	// has needed one, and a slice of one is harder to read than a pair.
	Interface string
	Port      int
}

// Revision is one published version of a service.
type Revision struct {
	Version string
	// Dir is the bundle directory Materialize writes, and the directory the
	// harness pushes from.
	Dir string
	// Deployed marks the revision a workload actually runs, which is what makes
	// an operational target expected for the service.
	Deployed bool
	// Files is the bundle content, path relative to Dir. Rendered through
	// text/template with .Domain bound to the fixture's OCI domain, so a
	// dependency ref can name the registry the harness happens to bring up.
	Files map[string]string
}

// SourceKind is the role a Data Source plays in the fixture.
type SourceKind string

const (
	// SourceRegistry is the OCI registry the fixture publishes to.
	SourceRegistry SourceKind = "registry"
	// SourceCache is the dashboard's on-disk OCI cache.
	SourceCache SourceKind = "cache"
	// SourceObservation is an operator-managed offline trace export.
	SourceObservation SourceKind = "observation"
	// SourceEvidence is the Evidence Server. It is proved by the target it
	// produced rather than on its own, so it is not counted as a source fact.
	SourceEvidence SourceKind = "evidence"
)

// Source is a Data Source identity the Product must publish.
type Source struct {
	ID   string
	Kind SourceKind
}

// Relationship is an edge between two services.
type Relationship struct {
	From, To string
	// Declared means the consumer's contract names the dependency.
	Declared bool
	// ObservedBy is the Data Source id whose export carries the call. Empty means
	// the edge is declared but never seen.
	ObservedBy string
	// Reconciliation is the verdict the backend must reach for the pair, e.g.
	// "matched".
	Reconciliation string
}

// Evidence is a target that arrives from a remote environment over the Evidence
// Server.
//
// Two identities meet here that a single "producer" field used to conflate, with
// the harness quietly picking a third value for the one that reaches the wire:
//
//	Source  WHERE the observations were collected — the environment. It is
//	        payload data: the EvidenceSet's Source and each observation's
//	        collector.
//	Signer  WHO signed the envelope. Producer is the id the envelope claims and
//	        the trust store binds the key to; KeyID selects that key.
//
// They can be the same string and in this fixture they are not, which is the
// point: the signed envelope now consumes both from here, so neither can be
// declared and then contradicted.
type Evidence struct {
	// Service is the subject of the envelope.
	Service string
	// Source is the environment the observations were collected in.
	Source string
	// Signer is the identity that signs the envelope carrying them.
	Signer Signer
	// ObservedAt is when the remote environment saw it, RFC3339.
	ObservedAt string
	// Via is the Data Source id the resulting target must be attributed to.
	Via string
}

// Signer is a producer identity and the key it signs with. The trust store binds
// the two: a public key filed under this producer authorizes exactly this
// producer id, so signing with a producer nobody generated a key for is rejected
// at ingestion rather than silently accepted.
type Signer struct {
	Producer string
	KeyID    string
}

// Journey names the parts the browser suite drives, so the discovered-keys
// handoff stays a projection of this value.
type Journey struct {
	// Provider is the service whose two revisions drive change analysis.
	Provider string
	// Consumer is the service that declares the dependency on Provider.
	Consumer string
	// External is the service that arrives through the Evidence Server.
	External string
}

// Service returns the named service.
func (s Scenario) Service(name string) (Service, bool) {
	for _, svc := range s.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return Service{}, false
}

// SourceID returns the id of the single source playing the given role.
func (s Scenario) SourceID(kind SourceKind) string {
	for _, src := range s.Sources {
		if src.Kind == kind {
			return src.ID
		}
	}
	return ""
}

// Validate refuses a fixture that cannot mean one thing.
//
// One rule, because one rule is what the surfaces disagreed about: Deployed
// means "the workload runs THIS". A service that runs something declares exactly
// one deployed revision; a service that runs nothing declares none.
//
// It is called by the projections and by the Product gate rather than left to
// DeployedRevision alone, because a surface only asks about the services it
// projects. Nothing asks a workload-less service what it deploys — so a Deployed
// flag on one used to sail through every projection untouched while still
// obliging the gate to find an operational target for a service the cluster had
// no reason to run.
//
// ponytail: one rule, checked where the fixture is read. Not a validation
// framework — everything else this fixture must satisfy is already proved by the
// counterexamples beside it.
func (s Scenario) Validate() error {
	for _, svc := range s.Services {
		if svc.Workload != nil {
			if _, err := svc.DeployedRevision(); err != nil {
				return fmt.Errorf("scenario %s: %w", s.Name, err)
			}
			continue
		}
		if deployed := svc.deployedRevisions(); len(deployed) > 0 {
			return fmt.Errorf("scenario %s: service %s runs no workload but marks %s deployed; nothing would run it, and no CR would ever pin it",
				s.Name, svc.Name, versionsOf(deployed))
		}
	}
	return nil
}

// DeployedRevision returns the ONE revision the service's workload runs.
//
// This used to be a scan returning the first revision flagged Deployed, and
// every surface ran it independently: a fixture declaring two deployments got a
// plan, a CR pinning the first and a gate proving that same first one — the
// second deployment erased, unanimously and in silence. Zero is the same failure
// from the other side: a CR that pins nothing. Neither is a revision this can
// return, so both are errors.
//
// Whether the service is deployed AT ALL is Workload, not this. A service that
// runs nothing has no deployed revision, so asking is the caller skipping its own
// Workload check, and the honest answer is an error rather than a zero Revision
// that reads like an answer.
func (svc Service) DeployedRevision() (Revision, error) {
	deployed := svc.deployedRevisions()
	switch {
	case svc.Workload == nil:
		return Revision{}, fmt.Errorf("service %s runs no workload, so no revision of it is deployed", svc.Name)
	case len(deployed) == 1:
		return deployed[0], nil
	case len(deployed) == 0:
		return Revision{}, fmt.Errorf("service %s declares a workload but deploys no revision, so its CR would pin nothing", svc.Name)
	default:
		return Revision{}, fmt.Errorf("service %s declares %d deployed revisions (%s); a workload runs exactly one, and pinning either would erase the other",
			svc.Name, len(deployed), versionsOf(deployed))
	}
}

// deployedRevisions is every revision the service flags Deployed.
func (svc Service) deployedRevisions() []Revision {
	var out []Revision
	for _, r := range svc.Revisions {
		if r.Deployed {
			out = append(out, r)
		}
	}
	return out
}

// versionsOf names revisions in a diagnostic, so a refusal says WHICH
// declarations contradict each other rather than only how many.
func versionsOf(revs []Revision) string {
	out := make([]string, len(revs))
	for i, r := range revs {
		out[i] = r.Version
	}
	return strings.Join(out, ", ")
}

// PublishedOnlyRevision returns a revision that exists in the registry but runs
// nowhere — the far side of a change analysis.
func (svc Service) PublishedOnlyRevision() (Revision, bool) {
	for _, r := range svc.Revisions {
		if !r.Deployed {
			return r, true
		}
	}
	return Revision{}, false
}

// FactCount is how many facts the live Product gate is obliged to prove for this
// scenario ON THE GIVEN SURFACE. It is derived rather than written down so the
// gate's progress line cannot claim a denominator the fixture stopped
// justifying.
//
// The surface is a parameter because the two surfaces owe different numbers of
// facts, and only one reason for that is legitimate: a capability the platform
// does not have. Compose has no controller, so nothing there can reconcile an
// operational target, and the target facts are not owed. Every other fact is.
// An unknown surface provides nothing, so it owes the smallest count — which is
// why the gate parses its surface rather than accepting a bare string.
func (s Scenario) FactCount(surface Surface) int {
	n := 1 // the round read every fact from ONE snapshot
	for _, src := range s.Sources {
		if src.Kind != SourceEvidence {
			n++ // the source is present and answered in full
		}
	}
	n++ // every declared service is in the snapshot, exactly once
	for _, svc := range s.Services {
		if svc.EvidenceOnly {
			continue
		}
		n += len(svc.Revisions) // one canonical, exact, retrievable revision each
		// A target is owed by RUNNING something, not by a Deployed flag. Counting
		// the flags would be this derivation resolving an ambiguous declaration on
		// its own — the thing Validate exists to stop — and it would hand the gate a
		// denominator no projection agreed to.
		if svc.Workload != nil && surface.Has(CapabilityOperationalTarget) {
			n++ // exactly one operational target, linking exactly to it
		}
	}
	n += 3 * len(s.Relationships) // declared, observed, and reconciled as one edge
	n += len(s.Evidence)          // the remote target survived enrichment
	return n
}

// Materialize writes every service's bundle directories under dir, rendering
// each file against the fixture's OCI domain.
func (s Scenario) Materialize(dir, domain string) error {
	if dir == "" {
		return fmt.Errorf("scenario %s: no output directory", s.Name)
	}
	if domain == "" {
		return fmt.Errorf("scenario %s: no OCI domain to render refs against", s.Name)
	}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			bundle := filepath.Join(dir, rev.Dir)
			if err := os.MkdirAll(bundle, 0o750); err != nil {
				return err
			}
			for _, name := range sortedKeys(rev.Files) {
				body, err := render(rev.Files[name], domain)
				if err != nil {
					return fmt.Errorf("%s/%s: %w", rev.Dir, name, err)
				}
				if err := os.WriteFile(filepath.Join(bundle, name), []byte(body), 0o600); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func render(body, domain string) (string, error) {
	t, err := template.New("f").Option("missingkey=error").Parse(body)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := t.Execute(&out, struct{ Domain string }{domain}); err != nil {
		return "", err
	}
	return out.String(), nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TraceExport renders the OTLP export the named observation source carries: one
// client span per relationship that source observes. The export is derived from
// the same relationships the gate later requires the backend to reconcile, so
// the observed half of the fixture cannot drift from the declared half.
func (s Scenario) TraceExport(source string) ([]byte, error) {
	byCaller := map[string][]string{}
	var callers []string
	for _, rel := range s.Relationships {
		if rel.ObservedBy != source {
			continue
		}
		if _, seen := byCaller[rel.From]; !seen {
			callers = append(callers, rel.From)
		}
		byCaller[rel.From] = append(byCaller[rel.From], rel.To)
	}
	if len(callers) == 0 {
		return nil, fmt.Errorf("scenario %s: no relationship is observed by %q", s.Name, source)
	}
	export := otlpExport{}
	for _, caller := range callers {
		spans := make([]otlpSpan, 0, len(byCaller[caller]))
		for _, callee := range byCaller[caller] {
			spans = append(spans, otlpSpan{
				Kind:       spanKindClient,
				Attributes: []otlpAttr{attr("peer.service", callee)},
			})
		}
		export.ResourceSpans = append(export.ResourceSpans, otlpResourceSpans{
			Resource:   otlpResource{Attributes: []otlpAttr{attr("service.name", caller)}},
			ScopeSpans: []otlpScopeSpans{{Spans: spans}},
		})
	}
	return json.Marshal(export)
}

// spanKindClient is OTLP's SPAN_KIND_CLIENT: the caller's side of a call, which
// is what makes peer.service the callee.
const spanKindClient = 3

type otlpExport struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpAttr `json:"attributes"`
}

type otlpScopeSpans struct {
	Spans []otlpSpan `json:"spans"`
}

type otlpSpan struct {
	Kind       int        `json:"kind"`
	Attributes []otlpAttr `json:"attributes"`
}

type otlpAttr struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue string `json:"stringValue"`
}

func attr(k, v string) otlpAttr {
	return otlpAttr{Key: k, Value: otlpValue{StringValue: v}}
}
