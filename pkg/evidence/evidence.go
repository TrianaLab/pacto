package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SubjectRef identifies the entity being observed.
type SubjectRef struct {
	Kind string
	Name string
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

// Provenance tracks how an observation was collected.
type Provenance struct {
	Collector  string
	DetectedAt time.Time
}

// Observation represents a single observed fact.
type Observation struct {
	Kind       ObservationKind
	Subject    SubjectRef
	Value      any
	Provenance Provenance
}

// EvidenceSet is a timestamped collection of observations about a service.
type EvidenceSet struct {
	Subject      SubjectRef
	ContractRef  string
	Source       string
	ObservedAt   time.Time
	Observations []Observation
}

// CapabilityObservation carries capability presence data.
type CapabilityObservation struct {
	Type    string
	Present bool
}

// WorkloadObservation carries workload type data.
type WorkloadObservation struct {
	Type string
}

// InterfaceObservation carries interface presence data.
type InterfaceObservation struct {
	Name    string
	Type    string
	Present bool
}

// DependencyObservation carries dependency reachability data.
type DependencyObservation struct {
	Name      string
	Reachable bool
}

// ConfigurationObservation carries configuration key presence data.
type ConfigurationObservation struct {
	Key     string
	Present bool
}

// PersistenceObservation carries persistence durability data.
type PersistenceObservation struct {
	Durable bool
}

// observationJSON is the JSON wire format for Observation.
type observationJSON struct {
	Kind       ObservationKind `json:"kind"`
	Subject    SubjectRef      `json:"subject"`
	Value      json.RawMessage `json:"value"`
	Provenance Provenance      `json:"provenance"`
}

// MarshalJSON implements custom JSON marshaling for Observation.
func (o Observation) MarshalJSON() ([]byte, error) {
	valueBytes, err := json.Marshal(o.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal observation value: %w", err)
	}
	return json.Marshal(observationJSON{
		Kind:       o.Kind,
		Subject:    o.Subject,
		Value:      valueBytes,
		Provenance: o.Provenance,
	})
}

// UnmarshalJSON implements custom JSON unmarshaling for Observation.
func (o *Observation) UnmarshalJSON(data []byte) error {
	var raw observationJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal observation: %w", err)
	}

	o.Kind = raw.Kind
	o.Subject = raw.Subject
	o.Provenance = raw.Provenance

	var target any
	switch raw.Kind {
	case CapabilityObserved:
		target = &CapabilityObservation{}
	case WorkloadObserved:
		target = &WorkloadObservation{}
	case InterfaceObserved:
		target = &InterfaceObservation{}
	case DependencyReachable:
		target = &DependencyObservation{}
	case ConfigurationPresent:
		target = &ConfigurationObservation{}
	case PersistenceObserved:
		target = &PersistenceObservation{}
	default:
		return fmt.Errorf("unknown observation kind: %s", raw.Kind)
	}

	if err := json.Unmarshal(raw.Value, target); err != nil {
		return fmt.Errorf("unmarshal %s value: %w", raw.Kind, err)
	}
	o.Value = target
	return nil
}

// NewCapabilityObserved constructs a capability observation.
func NewCapabilityObserved(subject SubjectRef, capType string, present bool, prov Provenance) Observation {
	return Observation{
		Kind:       CapabilityObserved,
		Subject:    subject,
		Value:      CapabilityObservation{Type: capType, Present: present},
		Provenance: prov,
	}
}

// NewWorkloadObserved constructs a workload observation.
func NewWorkloadObserved(subject SubjectRef, wType string, prov Provenance) Observation {
	return Observation{
		Kind:       WorkloadObserved,
		Subject:    subject,
		Value:      WorkloadObservation{Type: wType},
		Provenance: prov,
	}
}

// NewInterfaceObserved constructs an interface observation.
func NewInterfaceObserved(subject SubjectRef, name, iType string, present bool, prov Provenance) Observation {
	return Observation{
		Kind:       InterfaceObserved,
		Subject:    subject,
		Value:      InterfaceObservation{Name: name, Type: iType, Present: present},
		Provenance: prov,
	}
}

// NewDependencyReachable constructs a dependency reachability observation.
func NewDependencyReachable(subject SubjectRef, name string, reachable bool, prov Provenance) Observation {
	return Observation{
		Kind:       DependencyReachable,
		Subject:    subject,
		Value:      DependencyObservation{Name: name, Reachable: reachable},
		Provenance: prov,
	}
}

// NewConfigurationPresent constructs a configuration presence observation.
func NewConfigurationPresent(subject SubjectRef, key string, present bool, prov Provenance) Observation {
	return Observation{
		Kind:       ConfigurationPresent,
		Subject:    subject,
		Value:      ConfigurationObservation{Key: key, Present: present},
		Provenance: prov,
	}
}

// NewPersistenceObserved constructs a persistence observation.
func NewPersistenceObserved(subject SubjectRef, durable bool, prov Provenance) Observation {
	return Observation{
		Kind:       PersistenceObserved,
		Subject:    subject,
		Value:      PersistenceObservation{Durable: durable},
		Provenance: prov,
	}
}

// GetCapabilityObservation retrieves typed payload for CapabilityObserved.
func (o Observation) GetCapabilityObservation() (CapabilityObservation, error) {
	if o.Kind != CapabilityObserved {
		return CapabilityObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, CapabilityObserved)
	}
	v, ok := o.Value.(CapabilityObservation)
	if !ok {
		vp, ok := o.Value.(*CapabilityObservation)
		if !ok {
			return CapabilityObservation{}, fmt.Errorf("value is %T, not CapabilityObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// GetWorkloadObservation retrieves typed payload for WorkloadObserved.
func (o Observation) GetWorkloadObservation() (WorkloadObservation, error) {
	if o.Kind != WorkloadObserved {
		return WorkloadObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, WorkloadObserved)
	}
	v, ok := o.Value.(WorkloadObservation)
	if !ok {
		vp, ok := o.Value.(*WorkloadObservation)
		if !ok {
			return WorkloadObservation{}, fmt.Errorf("value is %T, not WorkloadObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// GetInterfaceObservation retrieves typed payload for InterfaceObserved.
func (o Observation) GetInterfaceObservation() (InterfaceObservation, error) {
	if o.Kind != InterfaceObserved {
		return InterfaceObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, InterfaceObserved)
	}
	v, ok := o.Value.(InterfaceObservation)
	if !ok {
		vp, ok := o.Value.(*InterfaceObservation)
		if !ok {
			return InterfaceObservation{}, fmt.Errorf("value is %T, not InterfaceObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// GetDependencyObservation retrieves typed payload for DependencyReachable.
func (o Observation) GetDependencyObservation() (DependencyObservation, error) {
	if o.Kind != DependencyReachable {
		return DependencyObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, DependencyReachable)
	}
	v, ok := o.Value.(DependencyObservation)
	if !ok {
		vp, ok := o.Value.(*DependencyObservation)
		if !ok {
			return DependencyObservation{}, fmt.Errorf("value is %T, not DependencyObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// GetConfigurationObservation retrieves typed payload for ConfigurationPresent.
func (o Observation) GetConfigurationObservation() (ConfigurationObservation, error) {
	if o.Kind != ConfigurationPresent {
		return ConfigurationObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, ConfigurationPresent)
	}
	v, ok := o.Value.(ConfigurationObservation)
	if !ok {
		vp, ok := o.Value.(*ConfigurationObservation)
		if !ok {
			return ConfigurationObservation{}, fmt.Errorf("value is %T, not ConfigurationObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// GetPersistenceObservation retrieves typed payload for PersistenceObserved.
func (o Observation) GetPersistenceObservation() (PersistenceObservation, error) {
	if o.Kind != PersistenceObserved {
		return PersistenceObservation{}, fmt.Errorf("observation kind is %s, not %s", o.Kind, PersistenceObserved)
	}
	v, ok := o.Value.(PersistenceObservation)
	if !ok {
		vp, ok := o.Value.(*PersistenceObservation)
		if !ok {
			return PersistenceObservation{}, fmt.Errorf("value is %T, not PersistenceObservation", o.Value)
		}
		return *vp, nil
	}
	return v, nil
}

// ValidateEvidenceSet checks structural validity of an EvidenceSet.
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
	for i, obs := range es.Observations {
		if err := validateObservation(obs); err != nil {
			errs = append(errs, fmt.Errorf("observation[%d]: %w", i, err))
		}
	}
	return errs
}

func validateObservation(obs Observation) error {
	if obs.Subject.Kind == "" || obs.Subject.Name == "" {
		return errors.New("subject kind or name is empty")
	}
	if obs.Provenance.Collector == "" {
		return errors.New("provenance collector is empty")
	}
	if obs.Provenance.DetectedAt.IsZero() {
		return errors.New("provenance detected at is zero")
	}

	switch obs.Kind {
	case CapabilityObserved:
		_, ok := obs.Value.(CapabilityObservation)
		if !ok {
			_, ok = obs.Value.(*CapabilityObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not CapabilityObservation", obs.Value)
		}
	case WorkloadObserved:
		_, ok := obs.Value.(WorkloadObservation)
		if !ok {
			_, ok = obs.Value.(*WorkloadObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not WorkloadObservation", obs.Value)
		}
	case InterfaceObserved:
		_, ok := obs.Value.(InterfaceObservation)
		if !ok {
			_, ok = obs.Value.(*InterfaceObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not InterfaceObservation", obs.Value)
		}
	case DependencyReachable:
		_, ok := obs.Value.(DependencyObservation)
		if !ok {
			_, ok = obs.Value.(*DependencyObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not DependencyObservation", obs.Value)
		}
	case ConfigurationPresent:
		_, ok := obs.Value.(ConfigurationObservation)
		if !ok {
			_, ok = obs.Value.(*ConfigurationObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not ConfigurationObservation", obs.Value)
		}
	case PersistenceObserved:
		_, ok := obs.Value.(PersistenceObservation)
		if !ok {
			_, ok = obs.Value.(*PersistenceObservation)
		}
		if !ok {
			return fmt.Errorf("value type is %T, not PersistenceObservation", obs.Value)
		}
	default:
		return fmt.Errorf("unknown observation kind: %s", obs.Kind)
	}
	return nil
}
