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
//	Plan              the machine-readable execution plan the shell harness consumes
//	PactoCRs          the cluster projection of the deployed targets, once digests exist
//	EvidencePayloads  the EvidenceSet each declared envelope carries
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
// Only projections with a consumer today exist. A Helm or Docker Compose
// projection would be a sibling of Materialize over the same value; the one-off
// Helm values the harness sets for the operator image, the insecure registry and
// the enabled components are NOT projected — they have one consumer and no
// counterpart anywhere else in the fixture, so declaring them would be uniformity
// for its own sake.
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

// DeployedRevision returns the revision a workload runs, if any.
func (svc Service) DeployedRevision() (Revision, bool) {
	for _, r := range svc.Revisions {
		if r.Deployed {
			return r, true
		}
	}
	return Revision{}, false
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
// scenario. It is derived rather than written down so the gate's progress line
// cannot claim a denominator the fixture stopped justifying.
func (s Scenario) FactCount() int {
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
		if _, ok := svc.DeployedRevision(); ok {
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
