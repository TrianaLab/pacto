package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func prov() Provenance                { return Provenance{Collector: "k8s-observer", DetectedAt: time.Unix(1, 0)} }
func sr(kind, name string) SubjectRef { return SubjectRef{Kind: kind, Name: name} }

// allObserved returns one Observed observation of every kind, with a correct Subject.Kind pairing.
func allObserved() []Observation {
	return []Observation{
		NewCapabilityObserved(SubjectRef{Kind: "capability", Name: "health"}, true, prov()),
		NewWorkloadObserved(SubjectRef{Kind: "service", Name: "orders"}, "service", prov()),
		NewInterfaceObserved(SubjectRef{Kind: "interface", Name: "public-api"}, "openapi", true, prov()),
		NewDependencyReachable(SubjectRef{Kind: "dependency", Name: "payments"}, true, prov()),
		NewConfigurationPresent(SubjectRef{Kind: "configuration", Name: "app"}, true, true, prov()),
		NewPersistenceObserved(SubjectRef{Kind: "service", Name: "orders"}, true, prov()),
	}
}

func TestRoundTrip_ObservedAndUnobserved(t *testing.T) {
	nonObserved := []Outcome{Unsupported, Failed, Stale, Insufficient}
	for _, o := range allObserved() {
		t.Run(string(o.Kind), func(t *testing.T) {
			testObservedRoundTrip(t, o)
			testUnobservedRoundTrip(t, o, nonObserved)
		})
	}
}

func testObservedRoundTrip(t *testing.T, o Observation) {
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"value"`) {
		t.Errorf("Observed must carry a value key: %s", data)
	}
	var back Observation
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Kind != o.Kind || back.Subject != o.Subject || back.Outcome != Observed {
		t.Errorf("round-trip envelope mismatch: %+v", back)
	}
	re, err := json.Marshal(back)
	if err != nil || string(re) != string(data) {
		t.Errorf("not byte-identical re-marshal")
	}
}

func testUnobservedRoundTrip(t *testing.T, o Observation, outcomes []Outcome) {
	for _, oc := range outcomes {
		t.Run(string(oc), func(t *testing.T) {
			u, err := NewUnobserved(o.Kind, o.Subject, oc, prov())
			if err != nil {
				t.Fatalf("NewUnobserved: %v", err)
			}
			ud, err := json.Marshal(u)
			if err != nil {
				t.Fatalf("marshal unobserved: %v", err)
			}
			if strings.Contains(string(ud), `"value"`) {
				t.Errorf("must NOT carry a value key: %s", ud)
			}
			var ub Observation
			if err := json.Unmarshal(ud, &ub); err != nil {
				t.Fatalf("unmarshal unobserved: %v", err)
			}
			if ub.Outcome != oc || len(ub.Value) != 0 {
				t.Errorf("round-trip mismatch: %+v", ub)
			}
		})
	}
}

func TestConfirmedAbsent_CarriesPayload(t *testing.T) {
	o := NewInterfaceObserved(SubjectRef{Kind: "interface", Name: "public-api"}, "openapi", false, prov())
	iface, err := o.GetInterfaceObservation()
	if err != nil || iface.Present {
		t.Fatalf("confirmed-absent interface: %+v err=%v", iface, err)
	}
	data, _ := json.Marshal(o)
	if !strings.Contains(string(data), `"present":false`) {
		t.Errorf("confirmed-absent must carry present:false: %s", data)
	}
}

func TestAllGetters(t *testing.T) {
	obs := allObserved()
	if c, err := obs[0].GetCapabilityObservation(); err != nil || !c.Present {
		t.Errorf("capability getter: %+v %v", c, err)
	}
	if w, err := obs[1].GetWorkloadObservation(); err != nil || w.Type != "service" {
		t.Errorf("workload getter: %+v %v", w, err)
	}
	if i, err := obs[2].GetInterfaceObservation(); err != nil || i.Type != "openapi" || !i.Present {
		t.Errorf("interface getter: %+v %v", i, err)
	}
	if d, err := obs[3].GetDependencyObservation(); err != nil || !d.Reachable {
		t.Errorf("dependency getter: %+v %v", d, err)
	}
	if cf, err := obs[4].GetConfigurationObservation(); err != nil || !cf.Present || !cf.Conformant {
		t.Errorf("configuration getter: %+v %v", cf, err)
	}
	if p, err := obs[5].GetPersistenceObservation(); err != nil || !p.Durable {
		t.Errorf("persistence getter: %+v %v", p, err)
	}
}

func TestGetterGuard(t *testing.T) {
	u, _ := NewUnobserved(CapabilityObserved, SubjectRef{Kind: "capability", Name: "health"}, Failed, prov())
	if _, err := u.GetCapabilityObservation(); err == nil {
		t.Error("getter must error for non-Observed outcome")
	}
	c := NewCapabilityObserved(SubjectRef{Kind: "capability", Name: "health"}, true, prov())
	if _, err := c.GetWorkloadObservation(); err == nil {
		t.Error("getter must error on kind mismatch")
	}
	// every getter errors on a kind mismatch, exercising each get[T] path
	if _, err := c.GetInterfaceObservation(); err == nil {
		t.Error("interface getter mismatch")
	}
	if _, err := c.GetDependencyObservation(); err == nil {
		t.Error("dependency getter mismatch")
	}
	if _, err := c.GetConfigurationObservation(); err == nil {
		t.Error("configuration getter mismatch")
	}
	if _, err := c.GetPersistenceObservation(); err == nil {
		t.Error("persistence getter mismatch")
	}
}

func TestUnmarshal_RejectsBadCombos(t *testing.T) {
	cases := []string{
		`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Failed","value":{"present":true},"provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`,
		`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Observed","provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`,
		`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"","provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`,
		`{"kind":"Nope","subject":{"kind":"x","name":"y"},"outcome":"Failed","provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`,
		`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Observed","value":{"nope":true},"provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`,
		`not json`, // fails in the json scanner before UnmarshalJSON is dispatched
		`123`,      // valid JSON token, wrong shape: reaches UnmarshalJSON and fails its inner decode
		// An unknown field inside an observation is rejected (review section S8): a
		// custom UnmarshalJSON does not inherit the outer decoder's strictness, so
		// the observation must decode strictly on its own.
		`{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Failed","provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"},"surprise":1}`,
	}
	for i, s := range cases {
		var o Observation
		if err := json.Unmarshal([]byte(s), &o); err == nil {
			t.Errorf("case %d: expected rejection, got nil", i)
		}
	}
}

func TestMarshal_RejectsInvalid(t *testing.T) {
	// A hand-built invalid Observation (Observed with no value) must fail Marshal.
	bad := Observation{Kind: CapabilityObserved, Subject: SubjectRef{Kind: "capability", Name: "h"}, Outcome: Observed}
	if _, err := json.Marshal(bad); err == nil {
		t.Error("marshal of Observed-without-value must fail")
	}
}

func TestSubjectKindPairing_INV1c(t *testing.T) {
	wrong := NewWorkloadObserved(SubjectRef{Kind: "workload", Name: "orders"}, "service", prov())
	if _, err := json.Marshal(wrong); err == nil {
		t.Error("WorkloadObserved with Subject.Kind=workload must be rejected (INV-1c)")
	}
	if _, err := NewUnobserved(InterfaceObserved, SubjectRef{Kind: "service", Name: "x"}, Failed, prov()); err == nil {
		t.Error("InterfaceObserved paired with service subject must be rejected (INV-1c)")
	}
	if _, err := NewUnobserved(WorkloadObserved, SubjectRef{Kind: "service", Name: "orders"}, Observed, prov()); err == nil {
		t.Error("NewUnobserved must reject Observed outcome")
	}
	if _, err := NewUnobserved("Bogus", SubjectRef{Kind: "x", Name: "y"}, Failed, prov()); err == nil {
		t.Error("NewUnobserved must reject unknown kind")
	}
}

func TestValidateEvidenceSet(t *testing.T) {
	good := EvidenceSet{
		Subject:      SubjectRef{Kind: "service", Name: "ns/orders"},
		ContractRef:  "oci://x",
		Source:       "k8s",
		ObservedAt:   time.Unix(1, 0),
		Observations: allObserved(),
	}
	if errs := ValidateEvidenceSet(good); len(errs) != 0 {
		t.Fatalf("good set: %v", errs)
	}
	// Empty top-level fields (5) + one observation hitting each per-observation branch:
	// empty subject, empty collector, zero detectedAt, and a validate() failure.
	bad := EvidenceSet{Observations: []Observation{
		{Kind: WorkloadObserved, Subject: SubjectRef{}, Outcome: Failed, Provenance: prov()},                                                  // empty subject
		{Kind: WorkloadObserved, Subject: SubjectRef{Kind: "service", Name: "s"}, Outcome: Failed},                                            // empty collector
		{Kind: PersistenceObserved, Subject: SubjectRef{Kind: "service", Name: "s"}, Outcome: Failed, Provenance: Provenance{Collector: "c"}}, // zero detectedAt (distinct identity)
		{Kind: CapabilityObserved, Subject: SubjectRef{Kind: "capability", Name: "h"}, Outcome: Observed, Provenance: prov()},                 // validate() fail: Observed no value
	}}
	errs := ValidateEvidenceSet(bad)
	if len(errs) < 9 { // 5 top-level + 4 per-observation
		t.Fatalf("bad set must produce >=9 errors, got %d: %v", len(errs), errs)
	}
}

func TestMustMarshal_PanicsOnUnmarshalable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mustMarshal must panic on an unmarshalable payload, not return empty evidence")
		}
	}()
	_ = mustMarshal(make(chan int)) // channels cannot be JSON-encoded
}

func TestValidateEvidenceSet_DuplicateIdentity(t *testing.T) {
	good := func(obs ...Observation) EvidenceSet {
		return EvidenceSet{Subject: sr("service", "ns/orders"), ContractRef: "o", Source: "k8s", ObservedAt: time.Unix(1, 0), Observations: obs}
	}
	iface := func(present bool) Observation {
		return NewInterfaceObserved(sr("interface", "public-api"), "openapi", present, prov())
	}
	// duplicate identical -> rejected
	if errs := ValidateEvidenceSet(good(iface(true), iface(true))); len(errs) == 0 {
		t.Error("duplicate identical observation must be rejected")
	}
	// duplicate with conflicting payload -> rejected (this is the dangerous case)
	if errs := ValidateEvidenceSet(good(iface(true), iface(false))); len(errs) == 0 {
		t.Error("duplicate conflicting-payload observation must be rejected")
	}
	// duplicate with different Outcome -> rejected
	uns, _ := NewUnobserved(InterfaceObserved, sr("interface", "public-api"), Failed, prov())
	if errs := ValidateEvidenceSet(good(iface(true), uns)); len(errs) == 0 {
		t.Error("duplicate identity with different Outcome must be rejected")
	}
	// same Subject.Name under different ObservationKind -> valid
	sameName := good(
		NewDependencyReachable(sr("dependency", "shared"), true, prov()),
		NewConfigurationPresent(sr("configuration", "shared"), true, true, prov()),
	)
	if errs := ValidateEvidenceSet(sameName); len(errs) != 0 {
		t.Errorf("same name under different kinds must be valid: %v", errs)
	}
	// workload + persistence share the service Subject but differ in Kind -> valid
	svcScoped := good(
		NewWorkloadObserved(sr("service", "orders"), "service", prov()),
		NewPersistenceObserved(sr("service", "orders"), true, prov()),
	)
	if errs := ValidateEvidenceSet(svcScoped); len(errs) != 0 {
		t.Errorf("workload+persistence sharing a service name must be valid: %v", errs)
	}
}

func TestConfigurationObservation_ConformantField(t *testing.T) {
	cases := []struct {
		name       string
		present    bool
		conformant bool
	}{
		{"absent", false, false},
		{"present-conformant", true, true},
		{"present-nonconformant", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := NewConfigurationPresent(sr("configuration", "app"), tc.present, tc.conformant, prov())
			data, err := json.Marshal(o)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var back Observation
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			cfg, err := back.GetConfigurationObservation()
			if err != nil {
				t.Fatalf("getter: %v", err)
			}
			if cfg.Present != tc.present || cfg.Conformant != tc.conformant {
				t.Errorf("want {present:%v conformant:%v}, got %+v", tc.present, tc.conformant, cfg)
			}
		})
	}
}

// Producers that do not set the runtime-conformance facts must emit exactly the JSON they emitted before
// those fields existed, so an older reader decoding strictly never sees a key it does not know.
func TestFactFields_OmittedWhenUnset_ByteIdentical(t *testing.T) {
	cases := map[string]struct {
		obs  Observation
		want string
	}{
		"interface":   {allObserved()[2], `{"type":"openapi","present":true}`},
		"dependency":  {allObserved()[3], `{"reachable":true}`},
		"persistence": {allObserved()[5], `{"durable":true}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if string(tc.obs.Value) != tc.want {
				t.Errorf("payload = %s, want %s", tc.obs.Value, tc.want)
			}
			data, err := json.Marshal(tc.obs)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			want := `{"kind":"` + string(tc.obs.Kind) + `","subject":{"kind":"` + tc.obs.Subject.Kind + `","name":"` +
				tc.obs.Subject.Name + `"},"outcome":"Observed","value":` + tc.want +
				`,"provenance":{"collector":"k8s-observer","detectedAt":"` + prov().DetectedAt.Format(time.RFC3339Nano) + `"}}`
			if string(data) != want {
				t.Errorf("marshal =\n%s\nwant\n%s", data, want)
			}
		})
	}
}

func TestFactFields_RoundTrip(t *testing.T) {
	shared := true
	iface := NewInterfaceObservation(sr("interface", "api"),
		InterfaceObservation{Type: "grpc", Present: true, Protocol: ProtocolHTTP2, Exposure: ExposureInternal}, prov())
	dep := NewDependencyObservation(sr("dependency", "billing"),
		DependencyObservation{Reachable: true, Version: "1.2.3", Undeclared: true}, prov())
	pers := NewPersistenceObservation(sr("service", "orders"),
		PersistenceObservation{Durable: true, Shared: &shared}, prov())

	for _, o := range []Observation{iface, dep, pers} {
		data, err := json.Marshal(o)
		if err != nil {
			t.Fatalf("marshal %s: %v", o.Kind, err)
		}
		var back Observation
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", o.Kind, err)
		}
		if string(back.Value) != string(o.Value) {
			t.Errorf("%s value = %s, want %s", o.Kind, back.Value, o.Value)
		}
	}
	if i, _ := iface.GetInterfaceObservation(); i.Protocol != ProtocolHTTP2 || i.Exposure != ExposureInternal {
		t.Errorf("interface facts lost: %+v", i)
	}
	if d, _ := dep.GetDependencyObservation(); d.Version != "1.2.3" || !d.Undeclared {
		t.Errorf("dependency facts lost: %+v", d)
	}
	if p, _ := pers.GetPersistenceObservation(); p.Shared == nil || !*p.Shared {
		t.Errorf("persistence facts lost: %+v", p)
	}
	// shared=false is a fact, not an absence: it must survive the wire as false, not vanish into nil.
	notShared := false
	local := NewPersistenceObservation(sr("service", "orders"), PersistenceObservation{Durable: true, Shared: &notShared}, prov())
	if string(local.Value) != `{"durable":true,"shared":false}` {
		t.Errorf("shared=false payload = %s", local.Value)
	}
}

// TestUnmarshal_RequiresVerdictKeys: an Observed payload missing (or nulling) a verdict-bearing key would
// decode to the zero value and read as a confirmed violation, so it is rejected; complete payloads pass.
func TestUnmarshal_RequiresVerdictKeys(t *testing.T) {
	obs := func(kind, subject, value string) string {
		return `{"kind":"` + kind + `","subject":{"kind":"` + subject + `","name":"x"},"outcome":"Observed","value":` + value +
			`,"provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`
	}
	reject := []string{
		obs("CapabilityObserved", "capability", `{}`),
		obs("CapabilityObserved", "capability", `{"present":null}`),
		obs("CapabilityObserved", "capability", `[]`),
		obs("CapabilityObserved", "capability", `{"present":true,"extra":1}`),
		obs("WorkloadObserved", "service", `{}`),
		obs("InterfaceObserved", "interface", `{"present":true}`),
		obs("InterfaceObserved", "interface", `{"type":"http"}`),
		obs("DependencyReachable", "dependency", `{"version":"1.0.0"}`),
		obs("ConfigurationPresent", "configuration", `{}`),
		obs("ConfigurationPresent", "configuration", `{"present":true}`),
		obs("PersistenceObserved", "service", `{"shared":true}`),
	}
	for i, s := range reject {
		var o Observation
		if err := json.Unmarshal([]byte(s), &o); err == nil {
			t.Errorf("reject case %d: expected rejection, got nil", i)
		}
	}
	accept := []string{
		obs("CapabilityObserved", "capability", `{"present":false}`),
		obs("WorkloadObserved", "service", `{"type":""}`),
		obs("InterfaceObserved", "interface", `{"type":"http","present":false}`),
		obs("DependencyReachable", "dependency", `{"reachable":false}`),
		obs("ConfigurationPresent", "configuration", `{"present":false}`),
		obs("ConfigurationPresent", "configuration", `{"present":true,"conformant":false}`),
		obs("PersistenceObserved", "service", `{"durable":false}`),
	}
	for i, s := range accept {
		var o Observation
		if err := json.Unmarshal([]byte(s), &o); err != nil {
			t.Errorf("accept case %d: %v", i, err)
		}
	}
	// First-party constructors emit every verdict key even when false (no omitempty), so they round-trip.
	s := SubjectRef{Kind: "configuration", Name: "x"}
	if _, err := json.Marshal(NewConfigurationPresent(s, true, false, prov())); err != nil {
		t.Errorf("constructor payload rejected: %v", err)
	}
}
