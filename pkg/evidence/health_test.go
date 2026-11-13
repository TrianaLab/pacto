package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func capabilityJSON(value string) string {
	return `{"kind":"CapabilityObserved","subject":{"kind":"capability","name":"health"},"outcome":"Observed","value":` +
		value + `,"provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`
}

func TestCapabilityFailingRoundTrip(t *testing.T) {
	o := NewCapabilityFailing(sr("capability", "health"), prov())
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var back Observation
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round trip %s: %v", data, err)
	}
	p, err := back.GetCapabilityObservation()
	if err != nil || !p.Present || p.Healthy == nil || *p.Healthy {
		t.Fatalf("payload %+v err=%v (from %s)", p, err, data)
	}
}

func TestCapabilityHealthyRequiresPresent(t *testing.T) {
	for _, v := range []string{`{"present":false,"healthy":false}`, `{"present":false,"healthy":true}`} {
		var o Observation
		if err := json.Unmarshal([]byte(capabilityJSON(v)), &o); err == nil {
			t.Errorf("%s: an absent capability must not report health", v)
		}
	}
	var o Observation
	if err := json.Unmarshal([]byte(capabilityJSON(`{"present":true,"healthy":true}`)), &o); err != nil {
		t.Errorf("present and healthy: %v", err)
	}
}

func TestCapabilityPayloadWithoutHealthyDecodes(t *testing.T) {
	var o Observation
	if err := json.Unmarshal([]byte(capabilityJSON(`{"present":true}`)), &o); err != nil {
		t.Fatal(err)
	}
	if p, _ := o.GetCapabilityObservation(); p.Healthy != nil {
		t.Errorf("healthy must stay unreported: %+v", p)
	}
	data, _ := json.Marshal(NewCapabilityObserved(sr("capability", "health"), true, prov()))
	if want := `{"present":true}`; !strings.Contains(string(data), `"value":`+want) {
		t.Errorf("an older payload must stay byte-identical: %s", data)
	}
}
