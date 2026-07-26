package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// FuzzObservationJSON asserts the JSON envelope is safe and total: any input is
// either rejected or accepted-and-valid, and every accepted observation round-trips
// and honors the Observed <=> exactly-one-payload invariant.
func FuzzObservationJSON(f *testing.F) {
	f.Add([]byte(`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Observed","value":{"present":true},"provenance":{"collector":"c","detectedAt":"2020-01-01T00:00:00Z"}}`))
	f.Add([]byte(`{"kind":"WorkloadObserved","subject":{"kind":"service","name":"svc"},"outcome":"Unsupported","provenance":{"collector":"c","detectedAt":"2020-01-01T00:00:00Z"}}`))
	f.Add([]byte(`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"h"},"outcome":"Observed","value":{"present":true,"extra":1},"provenance":{"collector":"c","detectedAt":"2020-01-01T00:00:00Z"}}`))
	f.Add([]byte(`garbage`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"h"},"outcome":"Observed","provenance":{"collector":"c","detectedAt":"2020-01-01T00:00:00Z"}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var o Observation
		if err := json.Unmarshal(data, &o); err != nil {
			return // malformed input is correctly rejected
		}
		// Accepted => passed validate() => Observed iff exactly one payload.
		if (o.Outcome == Observed) != (len(o.Value) > 0) {
			t.Fatalf("Observed<=>payload invariant violated: outcome=%s valueLen=%d", o.Outcome, len(o.Value))
		}
		// Accepted => must re-marshal and round-trip to an equivalent, still-valid value.
		out, err := json.Marshal(o)
		if err != nil {
			t.Fatalf("accepted observation failed to re-marshal: %v", err)
		}
		var o2 Observation
		if err := json.Unmarshal(out, &o2); err != nil {
			t.Fatalf("re-marshaled observation failed to round-trip: %v", err)
		}
		if o.Kind != o2.Kind || o.Outcome != o2.Outcome || o.Subject != o2.Subject {
			t.Fatalf("round-trip changed observation: %+v -> %+v", o, o2)
		}
	})
}

// fuzzKinds pairs an ObservationKind with a valid subject for constructing valid
// observations from fuzz-selected bytes.
var fuzzKinds = []ObservationKind{
	CapabilityObserved, InterfaceObserved, DependencyReachable,
	ConfigurationPresent, WorkloadObserved, PersistenceObserved,
}

func makeObs(k ObservationKind, name string) Observation {
	subj := SubjectRef{Kind: k.subjectKind(), Name: name}
	prov := Provenance{Collector: "c", DetectedAt: time.Unix(1, 0)}
	switch k {
	case CapabilityObserved:
		return NewCapabilityObserved(subj, true, prov)
	case InterfaceObserved:
		return NewInterfaceObserved(subj, "openapi", true, prov)
	case DependencyReachable:
		return NewDependencyReachable(subj, true, prov)
	case ConfigurationPresent:
		return NewConfigurationPresent(subj, true, true, prov)
	case WorkloadObserved:
		return NewWorkloadObserved(subj, "service", prov)
	default: // PersistenceObserved
		return NewPersistenceObserved(subj, true, prov)
	}
}

// FuzzEvidenceSetDuplicateIdentity proves ValidateEvidenceSet flags exactly the
// repeated assertion identities (INV-3): the count of duplicate-identity errors
// equals len(observations) - distinct(identities), regardless of array ordering.
func FuzzEvidenceSetDuplicateIdentity(f *testing.F) {
	f.Add([]byte{0, 1, 2})
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{3, 9, 3, 9})
	f.Add([]byte{})

	names := []string{"a", "b", "c"}
	f.Fuzz(func(t *testing.T, sel []byte) {
		es := EvidenceSet{
			Subject:     SubjectRef{Kind: "service", Name: "svc"},
			ContractRef: "oci://example/svc:1.0.0",
			Source:      "test",
			ObservedAt:  time.Unix(1, 0),
		}
		distinct := map[[3]string]bool{}
		expectedDups := 0
		for _, b := range sel {
			k := fuzzKinds[int(b)%len(fuzzKinds)]
			name := names[(int(b)/len(fuzzKinds))%len(names)]
			es.Observations = append(es.Observations, makeObs(k, name))
			key := [3]string{string(k), k.subjectKind(), name}
			if distinct[key] {
				expectedDups++
			}
			distinct[key] = true
		}

		gotDups := 0
		for _, err := range ValidateEvidenceSet(es) {
			if strings.Contains(err.Error(), "duplicate assertion identity") {
				gotDups++
			}
		}
		if gotDups != expectedDups {
			t.Fatalf("duplicate-identity errors = %d, want %d (n=%d distinct=%d)", gotDups, expectedDups, len(es.Observations), len(distinct))
		}
	})
}
