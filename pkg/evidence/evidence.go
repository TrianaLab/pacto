// Package evidence defines the Option A observation model: a discriminated Observation carrying an Outcome
// and a single json.RawMessage payload set iff Outcome == Observed. Assertion identity lives on SubjectRef
// (INV-1b); the Kind<->Subject.Kind pairing is enforced by validate() (INV-1c). Spec section 3.
package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/pkg/strictjson"
)

// SubjectRef identifies the specific entity an observation is about. For per-assertion kinds it names the
// assertion (capability type, interface name, dependency name, configuration name); for service-scoped
// kinds (workload, persistence) it names the service (c.Service.Name). Identity lives here, not in the
// payload, so a non-Observed observation is still attributable.
type SubjectRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Provenance tracks how an observation was collected.
type Provenance struct {
	Collector  string    `json:"collector"`
	DetectedAt time.Time `json:"detectedAt"`
}

// ObservationKind enumerates observation types.
type ObservationKind string

const (
	CapabilityObserved   ObservationKind = "CapabilityObserved"
	WorkloadObserved     ObservationKind = "WorkloadObserved"
	InterfaceObserved    ObservationKind = "InterfaceObserved"
	DependencyReachable  ObservationKind = "DependencyReachable"
	ConfigurationPresent ObservationKind = "ConfigurationPresent"
	PersistenceObserved  ObservationKind = "PersistenceObserved"
)

// Outcome is the collection verdict. Exactly Observed carries a payload. There is NO OutcomeAbsent.
type Outcome string

const (
	Observed     Outcome = "Observed"     // collected; Value present
	Unsupported  Outcome = "Unsupported"  // collector cannot observe this dimension
	Failed       Outcome = "Failed"       // collection attempted and errored
	Stale        Outcome = "Stale"        // last known data too old to trust
	Insufficient Outcome = "Insufficient" // partial/ambiguous, not conclusive (incl. within-window negatives)
)

func (o Outcome) valid() bool {
	switch o {
	case Observed, Unsupported, Failed, Stale, Insufficient:
		return true
	}
	return false
}

// Typed payloads carry observed FACTS only; identity is on SubjectRef (INV-1b migration).
type CapabilityObservation struct {
	Present bool `json:"present"`
}
type WorkloadObservation struct {
	Type string `json:"type"`
}
type InterfaceObservation struct {
	Type    string `json:"type"`
	Present bool   `json:"present"` // AVAILABILITY / reachability (B1)
	// Protocol is the wire protocol family a probe saw on the port; empty = not probed or no answer.
	Protocol Protocol `json:"protocol,omitempty"`
	// Exposure is how far the interface is reachable from; empty = unknown.
	Exposure Exposure `json:"exposure,omitempty"`
}
type DependencyObservation struct {
	Reachable bool `json:"reachable"`
	// Version is the provider's deployed contract version; empty = unknown.
	Version string `json:"version,omitempty"`
	// Undeclared marks a peer the producer resolved to a Pacto-managed service that matches no declared
	// dependency. Subject.Name then names that service, not a contract dependency.
	Undeclared bool `json:"undeclared,omitempty"`
}
type ConfigurationObservation struct {
	Present    bool `json:"present"`
	Conformant bool `json:"conformant"` // meaningful only when Present==true AND conformance was evaluated
}
type PersistenceObservation struct {
	Durable bool `json:"durable"`
	// Shared is true when one durable claim is mounted by more than one replica, false for per-replica
	// claims or a single replica, nil when unknown.
	Shared *bool `json:"shared,omitempty"`
}

// Observation is a single observed fact. Value holds the typed payload as raw JSON and is set iff
// Outcome == Observed. The Observed-implies-one-payload invariant is enforced by validate() at every
// JSON boundary.
type Observation struct {
	Kind       ObservationKind
	Subject    SubjectRef
	Outcome    Outcome
	Value      json.RawMessage // set iff Outcome == Observed
	Provenance Provenance
}

type observationJSON struct {
	Kind       ObservationKind `json:"kind"`
	Subject    SubjectRef      `json:"subject"`
	Outcome    Outcome         `json:"outcome"`
	Value      json.RawMessage `json:"value,omitempty"`
	Provenance Provenance      `json:"provenance"`
}

// EvidenceSet is a timestamped collection of observations about a service. Subject is the runtime TARGET
// identity (e.g. namespace/service) — never substituted for a per-observation assertion identity.
type EvidenceSet struct {
	Subject      SubjectRef
	ContractRef  string
	Source       string
	ObservedAt   time.Time
	Observations []Observation
}

// subjectKind is the expected Subject.Kind for an ObservationKind (INV-1c). Per-assertion kinds name their
// assertion; workload/persistence are service-scoped.
func (k ObservationKind) subjectKind() string {
	switch k {
	case CapabilityObserved:
		return "capability"
	case InterfaceObserved:
		return "interface"
	case DependencyReachable:
		return "dependency"
	case ConfigurationPresent:
		return "configuration"
	case WorkloadObserved, PersistenceObserved:
		return "service"
	}
	return ""
}

// validate enforces: Kind/Outcome known; Subject.Kind pairs with Kind (INV-1c); Observed <=> exactly one
// payload present AND decodable as the declared kind; non-Observed => no payload.
func (o Observation) validate() error {
	want := o.Kind.subjectKind()
	if want == "" {
		return fmt.Errorf("unknown observation kind: %q", o.Kind)
	}
	if !o.Outcome.valid() {
		return fmt.Errorf("unknown outcome: %q", o.Outcome)
	}
	if o.Subject.Kind != want {
		return fmt.Errorf("kind %s requires Subject.Kind %q, got %q (INV-1c)", o.Kind, want, o.Subject.Kind)
	}
	if o.Outcome == Observed {
		if len(o.Value) == 0 {
			return fmt.Errorf("outcome Observed requires a value payload for kind %s", o.Kind)
		}
		return o.checkKind()
	}
	if len(o.Value) != 0 {
		return fmt.Errorf("outcome %s must not carry a value payload", o.Outcome)
	}
	return nil
}

// verdictKeys lists the payload keys an Observed value must carry per kind. A missing or null key would
// decode to the zero value and read as a confirmed negative (`{}` would become Present=false), so it is
// rejected instead. ConfigurationPresent's conformant is only required when present is true: it is
// meaningless for an absent configuration. Optional keys (protocol, version, shared...) stay optional.
var verdictKeys = map[ObservationKind][]string{
	CapabilityObserved:   {"present"},
	WorkloadObserved:     {"type"},
	InterfaceObserved:    {"type", "present"},
	DependencyReachable:  {"reachable"},
	ConfigurationPresent: {"present"},
	PersistenceObserved:  {"durable"},
}

// checkKind proves Value decodes as the payload type for Kind. Rejects EXTRA fields and any payload that
// omits a verdict-bearing key (verdictKeys).
func (o Observation) checkKind() error {
	if err := o.checkVerdictKeys(); err != nil {
		return fmt.Errorf("value does not match kind %s: %w", o.Kind, err)
	}
	var err error
	switch o.Kind {
	case CapabilityObserved:
		_, err = decode[CapabilityObservation](o.Value)
	case WorkloadObserved:
		_, err = decode[WorkloadObservation](o.Value)
	case InterfaceObserved:
		_, err = decode[InterfaceObservation](o.Value)
	case DependencyReachable:
		_, err = decode[DependencyObservation](o.Value)
	case ConfigurationPresent:
		_, err = decode[ConfigurationObservation](o.Value)
	case PersistenceObserved:
		_, err = decode[PersistenceObservation](o.Value)
	}
	if err != nil {
		return fmt.Errorf("value does not match kind %s: %w", o.Kind, err)
	}
	return nil
}

func (o Observation) checkVerdictKeys() error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(o.Value, &keys); err != nil {
		return err
	}
	required := verdictKeys[o.Kind]
	if o.Kind == ConfigurationPresent && string(keys["present"]) == "true" {
		required = append(required, "conformant")
	}
	for _, k := range required {
		if v, ok := keys[k]; !ok || string(v) == "null" {
			return fmt.Errorf("missing required key %q", k)
		}
	}
	return nil
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

// MarshalJSON implements custom JSON marshaling for Observation, running validate() and omitting value for
// non-Observed outcomes.
func (o Observation) MarshalJSON() ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	j := observationJSON{Kind: o.Kind, Subject: o.Subject, Outcome: o.Outcome, Provenance: o.Provenance}
	if o.Outcome == Observed {
		j.Value = o.Value
	}
	return json.Marshal(j)
}

// UnmarshalJSON implements custom JSON unmarshaling for Observation, running validate() after decode.
func (o *Observation) UnmarshalJSON(data []byte) error {
	// A custom UnmarshalJSON does NOT inherit the outer decoder's
	// DisallowUnknownFields, so decode strictly here too — otherwise an unknown
	// field inside an observation is silently dropped even through a strict decode
	// of the enclosing EvidenceSet.
	var j observationJSON
	if err := strictjson.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("unmarshal observation: %w", err)
	}
	*o = Observation(j)
	return o.validate()
}

// mustMarshal marshals a payload struct. The current payload types are closed structs of primitives, so
// json.Marshal cannot fail today — but a future payload with a non-marshalable field must fail LOUDLY
// (a programmer bug), never silently produce empty/invalid evidence.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("evidence: marshal payload: %v", err))
	}
	return b
}

// NewCapabilityObserved constructs an Observed capability observation.
func NewCapabilityObserved(subject SubjectRef, present bool, prov Provenance) Observation {
	return Observation{Kind: CapabilityObserved, Subject: subject, Outcome: Observed,
		Value: mustMarshal(CapabilityObservation{Present: present}), Provenance: prov}
}

// NewWorkloadObserved constructs an Observed workload observation.
func NewWorkloadObserved(subject SubjectRef, wType string, prov Provenance) Observation {
	return Observation{Kind: WorkloadObserved, Subject: subject, Outcome: Observed,
		Value: mustMarshal(WorkloadObservation{Type: wType}), Provenance: prov}
}

// NewInterfaceObserved constructs an Observed interface observation carrying availability only.
func NewInterfaceObserved(subject SubjectRef, iType string, present bool, prov Provenance) Observation {
	return NewInterfaceObservation(subject, InterfaceObservation{Type: iType, Present: present}, prov)
}

// NewInterfaceObservation constructs an Observed interface observation from a full payload, for producers
// that also report Protocol or Exposure.
func NewInterfaceObservation(subject SubjectRef, p InterfaceObservation, prov Provenance) Observation {
	return Observation{Kind: InterfaceObserved, Subject: subject, Outcome: Observed,
		Value: mustMarshal(p), Provenance: prov}
}

// NewDependencyReachable constructs an Observed dependency observation carrying reachability only.
func NewDependencyReachable(subject SubjectRef, reachable bool, prov Provenance) Observation {
	return NewDependencyObservation(subject, DependencyObservation{Reachable: reachable}, prov)
}

// NewDependencyObservation constructs an Observed dependency observation from a full payload, for
// producers that also report Version or Undeclared.
func NewDependencyObservation(subject SubjectRef, p DependencyObservation, prov Provenance) Observation {
	return Observation{Kind: DependencyReachable, Subject: subject, Outcome: Observed,
		Value: mustMarshal(p), Provenance: prov}
}

// NewConfigurationPresent constructs an Observed configuration observation. When present is true,
// conformant indicates whether the config passed schema validation (meaningful only when evaluated).
func NewConfigurationPresent(subject SubjectRef, present bool, conformant bool, prov Provenance) Observation {
	return Observation{Kind: ConfigurationPresent, Subject: subject, Outcome: Observed,
		Value: mustMarshal(ConfigurationObservation{Present: present, Conformant: conformant}), Provenance: prov}
}

// NewPersistenceObserved constructs an Observed persistence observation carrying durability only.
func NewPersistenceObserved(subject SubjectRef, durable bool, prov Provenance) Observation {
	return NewPersistenceObservation(subject, PersistenceObservation{Durable: durable}, prov)
}

// NewPersistenceObservation constructs an Observed persistence observation from a full payload, for
// producers that also report Shared.
func NewPersistenceObservation(subject SubjectRef, p PersistenceObservation, prov Provenance) Observation {
	return Observation{Kind: PersistenceObserved, Subject: subject, Outcome: Observed,
		Value: mustMarshal(p), Provenance: prov}
}

// NewUnobserved builds a payload-less observation for any non-Observed outcome. Identity is carried on
// subject so the observation stays attributable (INV-1b); the Kind<->Subject.Kind pairing is enforced.
func NewUnobserved(kind ObservationKind, subject SubjectRef, outcome Outcome, prov Provenance) (Observation, error) {
	if outcome == Observed {
		return Observation{}, fmt.Errorf("NewUnobserved requires a non-Observed outcome, got %s", outcome)
	}
	o := Observation{Kind: kind, Subject: subject, Outcome: outcome, Provenance: prov}
	if err := o.validate(); err != nil {
		return Observation{}, err
	}
	return o, nil
}

func get[T any](o Observation, want ObservationKind) (T, error) {
	var zero T
	if o.Outcome != Observed {
		return zero, fmt.Errorf("observation outcome is %s (not Observed): no payload", o.Outcome)
	}
	if o.Kind != want {
		return zero, fmt.Errorf("observation kind is %s, not %s", o.Kind, want)
	}
	return decode[T](o.Value)
}

// GetCapabilityObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetCapabilityObservation() (CapabilityObservation, error) {
	return get[CapabilityObservation](o, CapabilityObserved)
}

// GetWorkloadObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetWorkloadObservation() (WorkloadObservation, error) {
	return get[WorkloadObservation](o, WorkloadObserved)
}

// GetInterfaceObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetInterfaceObservation() (InterfaceObservation, error) {
	return get[InterfaceObservation](o, InterfaceObserved)
}

// GetDependencyObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetDependencyObservation() (DependencyObservation, error) {
	return get[DependencyObservation](o, DependencyReachable)
}

// GetConfigurationObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetConfigurationObservation() (ConfigurationObservation, error) {
	return get[ConfigurationObservation](o, ConfigurationPresent)
}

// GetPersistenceObservation returns the typed payload iff Outcome == Observed and Kind matches.
func (o Observation) GetPersistenceObservation() (PersistenceObservation, error) {
	return get[PersistenceObservation](o, PersistenceObserved)
}

// ValidateEvidenceSet checks structural validity of an EvidenceSet: top-level identity/source/timestamp
// and each observation (Subject/Provenance non-empty plus the Observation invariant via validate()).
func ValidateEvidenceSet(es EvidenceSet) []error {
	var errs []error
	if es.Subject.Kind == "" {
		errs = append(errs, errors.New("subject kind is empty"))
	}
	if es.Subject.Name == "" {
		errs = append(errs, errors.New("subject name is empty"))
	}
	if es.ContractRef == "" {
		errs = append(errs, errors.New("contract ref is empty"))
	}
	if es.Source == "" {
		errs = append(errs, errors.New("source is empty"))
	}
	if es.ObservedAt.IsZero() {
		errs = append(errs, errors.New("observed at is zero"))
	}
	// Assertion identity (ObservationKind + Subject.Kind + Subject.Name) must appear at most once, so
	// findObservation is deterministic and contradictory evidence cannot hide behind array ordering
	// (INV-3). Workload and persistence share a service Subject but differ in Kind, so they do not collide.
	seen := make(map[[3]string]bool, len(es.Observations))
	for i, obs := range es.Observations {
		if obs.Subject.Kind == "" || obs.Subject.Name == "" {
			errs = append(errs, fmt.Errorf("observation[%d]: subject kind or name is empty", i))
			continue
		}
		key := [3]string{string(obs.Kind), obs.Subject.Kind, obs.Subject.Name}
		if seen[key] {
			errs = append(errs, fmt.Errorf("observation[%d]: duplicate assertion identity %s/%s/%s", i, obs.Kind, obs.Subject.Kind, obs.Subject.Name))
			continue
		}
		seen[key] = true
		if obs.Provenance.Collector == "" {
			errs = append(errs, fmt.Errorf("observation[%d]: provenance collector is empty", i))
			continue
		}
		if obs.Provenance.DetectedAt.IsZero() {
			errs = append(errs, fmt.Errorf("observation[%d]: provenance detected at is zero", i))
			continue
		}
		if err := obs.validate(); err != nil {
			errs = append(errs, fmt.Errorf("observation[%d]: %w", i, err))
		}
	}
	return errs
}
