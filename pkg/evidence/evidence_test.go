package evidence

import (
	"encoding/json"
	"testing"
	"time"
)

func TestObservation_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: now}

	tests := []struct {
		name string
		obs  Observation
	}{
		{
			name: "CapabilityObserved",
			obs:  NewCapabilityObserved(subj, "health", true, prov),
		},
		{
			name: "WorkloadObserved",
			obs:  NewWorkloadObserved(subj, "service", prov),
		},
		{
			name: "InterfaceObserved",
			obs:  NewInterfaceObserved(subj, "api", "http", true, prov),
		},
		{
			name: "DependencyReachable",
			obs:  NewDependencyReachable(subj, "database", false, prov),
		},
		{
			name: "ConfigurationPresent",
			obs:  NewConfigurationPresent(subj, "LOG_LEVEL", true, prov),
		},
		{
			name: "PersistenceObserved",
			obs:  NewPersistenceObserved(subj, true, prov),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.obs)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got Observation
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got.Kind != tt.obs.Kind {
				t.Errorf("kind = %v, want %v", got.Kind, tt.obs.Kind)
			}
			if got.Subject != tt.obs.Subject {
				t.Errorf("subject = %v, want %v", got.Subject, tt.obs.Subject)
			}
			if got.Provenance.Collector != tt.obs.Provenance.Collector {
				t.Errorf("collector = %v, want %v", got.Provenance.Collector, tt.obs.Provenance.Collector)
			}
			if !got.Provenance.DetectedAt.Equal(tt.obs.Provenance.DetectedAt) {
				t.Errorf("detected at = %v, want %v", got.Provenance.DetectedAt, tt.obs.Provenance.DetectedAt)
			}

			switch tt.obs.Kind {
			case CapabilityObserved:
				want := tt.obs.Value.(CapabilityObservation)
				gotVal, ok := got.Value.(*CapabilityObservation)
				if !ok {
					t.Fatalf("value type = %T, want *CapabilityObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			case WorkloadObserved:
				want := tt.obs.Value.(WorkloadObservation)
				gotVal, ok := got.Value.(*WorkloadObservation)
				if !ok {
					t.Fatalf("value type = %T, want *WorkloadObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			case InterfaceObserved:
				want := tt.obs.Value.(InterfaceObservation)
				gotVal, ok := got.Value.(*InterfaceObservation)
				if !ok {
					t.Fatalf("value type = %T, want *InterfaceObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			case DependencyReachable:
				want := tt.obs.Value.(DependencyObservation)
				gotVal, ok := got.Value.(*DependencyObservation)
				if !ok {
					t.Fatalf("value type = %T, want *DependencyObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			case ConfigurationPresent:
				want := tt.obs.Value.(ConfigurationObservation)
				gotVal, ok := got.Value.(*ConfigurationObservation)
				if !ok {
					t.Fatalf("value type = %T, want *ConfigurationObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			case PersistenceObserved:
				want := tt.obs.Value.(PersistenceObservation)
				gotVal, ok := got.Value.(*PersistenceObservation)
				if !ok {
					t.Fatalf("value type = %T, want *PersistenceObservation", got.Value)
				}
				if *gotVal != want {
					t.Errorf("value = %+v, want %+v", *gotVal, want)
				}
			}
		})
	}
}

func TestEvidenceSet_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: now}

	es := EvidenceSet{
		Subject:     subj,
		ContractRef: "payments@1.0.0",
		Source:      "kubernetes",
		ObservedAt:  now,
		Observations: []Observation{
			NewCapabilityObserved(subj, "health", true, prov),
			NewWorkloadObserved(subj, "service", prov),
			NewInterfaceObserved(subj, "api", "http", true, prov),
			NewDependencyReachable(subj, "database", false, prov),
			NewConfigurationPresent(subj, "LOG_LEVEL", true, prov),
			NewPersistenceObserved(subj, true, prov),
		},
	}

	data, err := json.Marshal(es)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got EvidenceSet
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Subject != es.Subject {
		t.Errorf("subject = %v, want %v", got.Subject, es.Subject)
	}
	if got.ContractRef != es.ContractRef {
		t.Errorf("contract ref = %v, want %v", got.ContractRef, es.ContractRef)
	}
	if got.Source != es.Source {
		t.Errorf("source = %v, want %v", got.Source, es.Source)
	}
	if !got.ObservedAt.Equal(es.ObservedAt) {
		t.Errorf("observed at = %v, want %v", got.ObservedAt, es.ObservedAt)
	}
	if len(got.Observations) != len(es.Observations) {
		t.Fatalf("observations count = %d, want %d", len(got.Observations), len(es.Observations))
	}

	for i, obs := range got.Observations {
		if obs.Kind != es.Observations[i].Kind {
			t.Errorf("observation[%d] kind = %v, want %v", i, obs.Kind, es.Observations[i].Kind)
		}
	}

	cap, err := got.Observations[0].GetCapabilityObservation()
	if err != nil {
		t.Errorf("get capability: %v", err)
	}
	if cap.Type != "health" || !cap.Present {
		t.Errorf("capability = %+v, want {health true}", cap)
	}

	wl, err := got.Observations[1].GetWorkloadObservation()
	if err != nil {
		t.Errorf("get workload: %v", err)
	}
	if wl.Type != "service" {
		t.Errorf("workload type = %v, want service", wl.Type)
	}

	iface, err := got.Observations[2].GetInterfaceObservation()
	if err != nil {
		t.Errorf("get interface: %v", err)
	}
	if iface.Name != "api" || iface.Type != "http" || !iface.Present {
		t.Errorf("interface = %+v, want {api http true}", iface)
	}

	dep, err := got.Observations[3].GetDependencyObservation()
	if err != nil {
		t.Errorf("get dependency: %v", err)
	}
	if dep.Name != "database" || dep.Reachable {
		t.Errorf("dependency = %+v, want {database false}", dep)
	}

	cfg, err := got.Observations[4].GetConfigurationObservation()
	if err != nil {
		t.Errorf("get configuration: %v", err)
	}
	if cfg.Key != "LOG_LEVEL" || !cfg.Present {
		t.Errorf("configuration = %+v, want {LOG_LEVEL true}", cfg)
	}

	pers, err := got.Observations[5].GetPersistenceObservation()
	if err != nil {
		t.Errorf("get persistence: %v", err)
	}
	if !pers.Durable {
		t.Errorf("persistence durable = %v, want true", pers.Durable)
	}
}

func TestValidateEvidenceSet(t *testing.T) {
	now := time.Now().UTC()
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: now}

	tests := []struct {
		name    string
		es      EvidenceSet
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
				Observations: []Observation{
					NewCapabilityObserved(subj, "health", true, prov),
				},
			},
			wantErr: false,
		},
		{
			name: "empty subject kind",
			es: EvidenceSet{
				Subject:     SubjectRef{Kind: "", Name: "payments"},
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
			},
			wantErr: true,
			errMsg:  "subject kind",
		},
		{
			name: "empty subject name",
			es: EvidenceSet{
				Subject:     SubjectRef{Kind: "Service", Name: ""},
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
			},
			wantErr: true,
			errMsg:  "subject name",
		},
		{
			name: "empty contract ref",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "",
				Source:      "kubernetes",
				ObservedAt:  now,
			},
			wantErr: true,
			errMsg:  "contract ref",
		},
		{
			name: "empty source",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "",
				ObservedAt:  now,
			},
			wantErr: true,
			errMsg:  "source",
		},
		{
			name: "zero observed at",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  time.Time{},
			},
			wantErr: true,
			errMsg:  "observed at",
		},
		{
			name: "invalid observation subject",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
				Observations: []Observation{
					{
						Kind:       CapabilityObserved,
						Subject:    SubjectRef{Kind: "", Name: ""},
						Value:      CapabilityObservation{Type: "health", Present: true},
						Provenance: prov,
					},
				},
			},
			wantErr: true,
			errMsg:  "subject kind or name",
		},
		{
			name: "invalid observation provenance",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
				Observations: []Observation{
					{
						Kind:       CapabilityObserved,
						Subject:    subj,
						Value:      CapabilityObservation{Type: "health", Present: true},
						Provenance: Provenance{Collector: "", DetectedAt: now},
					},
				},
			},
			wantErr: true,
			errMsg:  "provenance collector",
		},
		{
			name: "wrong value type",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
				Observations: []Observation{
					{
						Kind:       CapabilityObserved,
						Subject:    subj,
						Value:      WorkloadObservation{Type: "service"},
						Provenance: prov,
					},
				},
			},
			wantErr: true,
			errMsg:  "value type",
		},
		{
			name: "unknown kind",
			es: EvidenceSet{
				Subject:     subj,
				ContractRef: "payments@1.0.0",
				Source:      "kubernetes",
				ObservedAt:  now,
				Observations: []Observation{
					{
						Kind:       "UnknownKind",
						Subject:    subj,
						Value:      CapabilityObservation{Type: "health", Present: true},
						Provenance: prov,
					},
				},
			},
			wantErr: true,
			errMsg:  "unknown observation kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateEvidenceSet(tt.es)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				found := false
				for _, e := range errs {
					if contains(e.Error(), tt.errMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, got %v", tt.errMsg, errs)
				}
			} else {
				if len(errs) > 0 {
					t.Errorf("unexpected error: %v", errs)
				}
			}
		})
	}
}

func TestConstructors(t *testing.T) {
	now := time.Now().UTC()
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: now}

	tests := []struct {
		name     string
		obs      Observation
		wantKind ObservationKind
	}{
		{
			name:     "NewCapabilityObserved",
			obs:      NewCapabilityObserved(subj, "health", true, prov),
			wantKind: CapabilityObserved,
		},
		{
			name:     "NewWorkloadObserved",
			obs:      NewWorkloadObserved(subj, "service", prov),
			wantKind: WorkloadObserved,
		},
		{
			name:     "NewInterfaceObserved",
			obs:      NewInterfaceObserved(subj, "api", "http", true, prov),
			wantKind: InterfaceObserved,
		},
		{
			name:     "NewDependencyReachable",
			obs:      NewDependencyReachable(subj, "db", false, prov),
			wantKind: DependencyReachable,
		},
		{
			name:     "NewConfigurationPresent",
			obs:      NewConfigurationPresent(subj, "LOG", true, prov),
			wantKind: ConfigurationPresent,
		},
		{
			name:     "NewPersistenceObserved",
			obs:      NewPersistenceObserved(subj, true, prov),
			wantKind: PersistenceObserved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.obs.Kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", tt.obs.Kind, tt.wantKind)
			}
			if tt.obs.Subject != subj {
				t.Errorf("subject = %v, want %v", tt.obs.Subject, subj)
			}
			if tt.obs.Provenance != prov {
				t.Errorf("provenance = %v, want %v", tt.obs.Provenance, prov)
			}
		})
	}
}

func TestGetters_WrongKind(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	cap := NewCapabilityObserved(subj, "health", true, prov)
	wl := NewWorkloadObserved(subj, "service", prov)

	_, err := cap.GetWorkloadObservation()
	if err == nil {
		t.Error("expected error for wrong kind")
	}

	_, err = wl.GetCapabilityObservation()
	if err == nil {
		t.Error("expected error for wrong kind on CapabilityObserved")
	}
}

func TestGetters_WrongValueType(t *testing.T) {
	obs := Observation{
		Kind:       CapabilityObserved,
		Subject:    SubjectRef{Kind: "Service", Name: "payments"},
		Value:      "wrong",
		Provenance: Provenance{Collector: "k8s", DetectedAt: time.Now()},
	}

	_, err := obs.GetCapabilityObservation()
	if err == nil {
		t.Error("expected error for wrong value type")
	}
}

func TestObservation_UnmarshalJSON_UnknownKind(t *testing.T) {
	data := []byte(`{"kind":"UnknownKind","subject":{"Kind":"Service","Name":"payments"},"value":{},"provenance":{"Collector":"k8s","DetectedAt":"2026-07-24T00:00:00Z"}}`)
	var obs Observation
	err := obs.UnmarshalJSON(data)
	if err == nil || !contains(err.Error(), "unknown observation kind") {
		t.Errorf("expected unknown kind error, got %v", err)
	}
}

func TestObservation_UnmarshalJSON_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid}`)
	var obs Observation
	err := obs.UnmarshalJSON(data)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestObservation_MarshalJSON_InvalidValue(t *testing.T) {
	obs := Observation{
		Kind:    CapabilityObserved,
		Subject: SubjectRef{Kind: "Service", Name: "payments"},
		Value:   make(chan int),
		Provenance: Provenance{
			Collector:  "k8s",
			DetectedAt: time.Now(),
		},
	}
	_, err := obs.MarshalJSON()
	if err == nil {
		t.Error("expected error for invalid value type")
	}
}

func TestGetters_PointerVariant(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	data := []byte(`{"kind":"CapabilityObserved","subject":{"Kind":"Service","Name":"payments"},"value":{"Type":"health","Present":true},"provenance":{"Collector":"k8s","DetectedAt":"2026-07-24T00:00:00Z"}}`)
	var obs Observation
	if err := json.Unmarshal(data, &obs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cap, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Errorf("get capability (pointer variant): %v", err)
	}
	if cap.Type != "health" || !cap.Present {
		t.Errorf("capability = %+v, want {health true}", cap)
	}

	obs2 := NewWorkloadObserved(subj, "service", prov)
	data2, _ := json.Marshal(obs2)
	var obs3 Observation
	if err := json.Unmarshal(data2, &obs3); err != nil {
		t.Fatal(err)
	}
	wl, err := obs3.GetWorkloadObservation()
	if err != nil {
		t.Errorf("get workload (pointer variant): %v", err)
	}
	if wl.Type != "service" {
		t.Errorf("workload type = %v, want service", wl.Type)
	}

	obs4 := NewInterfaceObserved(subj, "api", "http", true, prov)
	data4, _ := json.Marshal(obs4)
	var obs5 Observation
	if err := json.Unmarshal(data4, &obs5); err != nil {
		t.Fatal(err)
	}
	iface, err := obs5.GetInterfaceObservation()
	if err != nil {
		t.Errorf("get interface (pointer variant): %v", err)
	}
	if iface.Name != "api" {
		t.Errorf("interface name = %v, want api", iface.Name)
	}

	obs6 := NewDependencyReachable(subj, "db", false, prov)
	data6, _ := json.Marshal(obs6)
	var obs7 Observation
	if err := json.Unmarshal(data6, &obs7); err != nil {
		t.Fatal(err)
	}
	dep, err := obs7.GetDependencyObservation()
	if err != nil {
		t.Errorf("get dependency (pointer variant): %v", err)
	}
	if dep.Name != "db" {
		t.Errorf("dependency name = %v, want db", dep.Name)
	}

	obs8 := NewConfigurationPresent(subj, "LOG", true, prov)
	data8, _ := json.Marshal(obs8)
	var obs9 Observation
	if err := json.Unmarshal(data8, &obs9); err != nil {
		t.Fatal(err)
	}
	cfg, err := obs9.GetConfigurationObservation()
	if err != nil {
		t.Errorf("get configuration (pointer variant): %v", err)
	}
	if cfg.Key != "LOG" {
		t.Errorf("configuration key = %v, want LOG", cfg.Key)
	}

	obs10 := NewPersistenceObserved(subj, true, prov)
	data10, _ := json.Marshal(obs10)
	var obs11 Observation
	if err := json.Unmarshal(data10, &obs11); err != nil {
		t.Fatal(err)
	}
	pers, err := obs11.GetPersistenceObservation()
	if err != nil {
		t.Errorf("get persistence (pointer variant): %v", err)
	}
	if !pers.Durable {
		t.Errorf("persistence durable = %v, want true", pers.Durable)
	}
}

func TestObservation_UnmarshalJSON_InvalidValueJSON(t *testing.T) {
	data := []byte(`{"kind":"CapabilityObserved","subject":{"Kind":"Service","Name":"payments"},"value":"invalid","provenance":{"Collector":"k8s","DetectedAt":"2026-07-24T00:00:00Z"}}`)
	var obs Observation
	err := obs.UnmarshalJSON(data)
	if err == nil || !contains(err.Error(), "unmarshal CapabilityObserved value") {
		t.Errorf("expected value unmarshal error, got %v", err)
	}
}

func TestValidateEvidenceSet_ZeroProvenanceDetectedAt(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	es := EvidenceSet{
		Subject:     subj,
		ContractRef: "payments@1.0.0",
		Source:      "kubernetes",
		ObservedAt:  time.Now(),
		Observations: []Observation{
			{
				Kind:       CapabilityObserved,
				Subject:    subj,
				Value:      CapabilityObservation{Type: "health", Present: true},
				Provenance: Provenance{Collector: "k8s", DetectedAt: time.Time{}},
			},
		},
	}
	errs := ValidateEvidenceSet(es)
	if len(errs) == 0 {
		t.Fatal("expected error for zero provenance detected at")
	}
	found := false
	for _, e := range errs {
		if contains(e.Error(), "provenance detected at") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error containing 'provenance detected at', got %v", errs)
	}
}

func TestValidateObservation_AllKinds(t *testing.T) {
	now := time.Now().UTC()
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: now}

	tests := []struct {
		name    string
		obs     Observation
		wantErr string
	}{
		{
			name:    "CapabilityObserved valid",
			obs:     NewCapabilityObserved(subj, "health", true, prov),
			wantErr: "",
		},
		{
			name: "CapabilityObserved wrong value type",
			obs: Observation{
				Kind:       CapabilityObserved,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
		{
			name:    "WorkloadObserved valid",
			obs:     NewWorkloadObserved(subj, "service", prov),
			wantErr: "",
		},
		{
			name: "WorkloadObserved wrong value type",
			obs: Observation{
				Kind:       WorkloadObserved,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
		{
			name:    "InterfaceObserved valid",
			obs:     NewInterfaceObserved(subj, "api", "http", true, prov),
			wantErr: "",
		},
		{
			name: "InterfaceObserved wrong value type",
			obs: Observation{
				Kind:       InterfaceObserved,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
		{
			name:    "DependencyReachable valid",
			obs:     NewDependencyReachable(subj, "db", false, prov),
			wantErr: "",
		},
		{
			name: "DependencyReachable wrong value type",
			obs: Observation{
				Kind:       DependencyReachable,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
		{
			name:    "ConfigurationPresent valid",
			obs:     NewConfigurationPresent(subj, "LOG", true, prov),
			wantErr: "",
		},
		{
			name: "ConfigurationPresent wrong value type",
			obs: Observation{
				Kind:       ConfigurationPresent,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
		{
			name:    "PersistenceObserved valid",
			obs:     NewPersistenceObserved(subj, true, prov),
			wantErr: "",
		},
		{
			name: "PersistenceObserved wrong value type",
			obs: Observation{
				Kind:       PersistenceObserved,
				Subject:    subj,
				Value:      "wrong",
				Provenance: prov,
			},
			wantErr: "value type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateObservation(tt.obs)
			if tt.wantErr != "" {
				if err == nil || !contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGetters_ValueVariant(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	obs1 := Observation{
		Kind:       CapabilityObserved,
		Subject:    subj,
		Value:      CapabilityObservation{Type: "health", Present: true},
		Provenance: prov,
	}
	cap, err := obs1.GetCapabilityObservation()
	if err != nil {
		t.Errorf("get capability (value variant): %v", err)
	}
	if cap.Type != "health" {
		t.Errorf("capability type = %v, want health", cap.Type)
	}

	obs2 := Observation{
		Kind:       WorkloadObserved,
		Subject:    subj,
		Value:      WorkloadObservation{Type: "service"},
		Provenance: prov,
	}
	wl, err := obs2.GetWorkloadObservation()
	if err != nil {
		t.Errorf("get workload (value variant): %v", err)
	}
	if wl.Type != "service" {
		t.Errorf("workload type = %v, want service", wl.Type)
	}

	obs3 := Observation{
		Kind:       InterfaceObserved,
		Subject:    subj,
		Value:      InterfaceObservation{Name: "api", Type: "http", Present: true},
		Provenance: prov,
	}
	iface, err := obs3.GetInterfaceObservation()
	if err != nil {
		t.Errorf("get interface (value variant): %v", err)
	}
	if iface.Name != "api" {
		t.Errorf("interface name = %v, want api", iface.Name)
	}

	obs4 := Observation{
		Kind:       DependencyReachable,
		Subject:    subj,
		Value:      DependencyObservation{Name: "db", Reachable: false},
		Provenance: prov,
	}
	dep, err := obs4.GetDependencyObservation()
	if err != nil {
		t.Errorf("get dependency (value variant): %v", err)
	}
	if dep.Name != "db" {
		t.Errorf("dependency name = %v, want db", dep.Name)
	}

	obs5 := Observation{
		Kind:       ConfigurationPresent,
		Subject:    subj,
		Value:      ConfigurationObservation{Key: "LOG", Present: true},
		Provenance: prov,
	}
	cfg, err := obs5.GetConfigurationObservation()
	if err != nil {
		t.Errorf("get configuration (value variant): %v", err)
	}
	if cfg.Key != "LOG" {
		t.Errorf("configuration key = %v, want LOG", cfg.Key)
	}

	obs6 := Observation{
		Kind:       PersistenceObserved,
		Subject:    subj,
		Value:      PersistenceObservation{Durable: true},
		Provenance: prov,
	}
	pers, err := obs6.GetPersistenceObservation()
	if err != nil {
		t.Errorf("get persistence (value variant): %v", err)
	}
	if !pers.Durable {
		t.Errorf("persistence durable = %v, want true", pers.Durable)
	}
}

func TestGetters_AllWrongKind(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	obs := NewCapabilityObserved(subj, "health", true, prov)

	_, err := obs.GetWorkloadObservation()
	if err == nil || !contains(err.Error(), "observation kind") {
		t.Errorf("GetWorkloadObservation: expected kind error, got %v", err)
	}

	_, err = obs.GetInterfaceObservation()
	if err == nil || !contains(err.Error(), "observation kind") {
		t.Errorf("GetInterfaceObservation: expected kind error, got %v", err)
	}

	_, err = obs.GetDependencyObservation()
	if err == nil || !contains(err.Error(), "observation kind") {
		t.Errorf("GetDependencyObservation: expected kind error, got %v", err)
	}

	_, err = obs.GetConfigurationObservation()
	if err == nil || !contains(err.Error(), "observation kind") {
		t.Errorf("GetConfigurationObservation: expected kind error, got %v", err)
	}

	_, err = obs.GetPersistenceObservation()
	if err == nil || !contains(err.Error(), "observation kind") {
		t.Errorf("GetPersistenceObservation: expected kind error, got %v", err)
	}
}

func TestGetters_AllWrongValuePointer(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	tests := []struct {
		name    string
		obs     Observation
		getFunc func(Observation) (any, error)
	}{
		{
			name: "CapabilityObserved",
			obs: Observation{
				Kind:       CapabilityObserved,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetCapabilityObservation()
			},
		},
		{
			name: "WorkloadObserved",
			obs: Observation{
				Kind:       WorkloadObserved,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetWorkloadObservation()
			},
		},
		{
			name: "InterfaceObserved",
			obs: Observation{
				Kind:       InterfaceObserved,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetInterfaceObservation()
			},
		},
		{
			name: "DependencyReachable",
			obs: Observation{
				Kind:       DependencyReachable,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetDependencyObservation()
			},
		},
		{
			name: "ConfigurationPresent",
			obs: Observation{
				Kind:       ConfigurationPresent,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetConfigurationObservation()
			},
		},
		{
			name: "PersistenceObserved",
			obs: Observation{
				Kind:       PersistenceObserved,
				Subject:    subj,
				Value:      new(int),
				Provenance: prov,
			},
			getFunc: func(o Observation) (any, error) {
				return o.GetPersistenceObservation()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.getFunc(tt.obs)
			if err == nil || !contains(err.Error(), "value is") {
				t.Errorf("expected value type error, got %v", err)
			}
		})
	}
}

func TestGetters_PointerCorrectType(t *testing.T) {
	subj := SubjectRef{Kind: "Service", Name: "payments"}
	prov := Provenance{Collector: "k8s", DetectedAt: time.Now()}

	tests := []struct {
		name  string
		obs   Observation
		check func(t *testing.T, obs Observation)
	}{
		{
			name: "CapabilityObserved pointer",
			obs: Observation{
				Kind:       CapabilityObserved,
				Subject:    subj,
				Value:      &CapabilityObservation{Type: "health", Present: true},
				Provenance: prov,
			},
			check: func(t *testing.T, obs Observation) {
				cap, err := obs.GetCapabilityObservation()
				if err != nil {
					t.Errorf("get capability (correct pointer): %v", err)
				}
				if cap.Type != "health" || !cap.Present {
					t.Errorf("capability = %+v, want {health true}", cap)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.obs)
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexContains(s, substr) >= 0)
}

func indexContains(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
