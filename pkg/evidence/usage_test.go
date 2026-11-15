package evidence

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// usageJSON is a ResourceUsage observation with the given outcome and raw value payload.
func usageJSON(outcome, value string) string {
	v := ""
	if value != "" {
		v = `"value":` + value + `,`
	}
	return `{"kind":"ResourceUsage","subject":{"kind":"configuration","name":"db"},"outcome":"` + outcome + `",` + v +
		`"provenance":{"collector":"c","detectedAt":"2026-01-01T00:00:00Z"}}`
}

func decodeUsage(s string) error {
	var o Observation
	return json.Unmarshal([]byte(s), &o)
}

func TestUsageStrictDecodeRejectsUnknownKey(t *testing.T) {
	if err := decodeUsage(usageJSON("Observed", `{"provider":"acme-db","metrics":[{"name":"storage-used","value":31,"unit":"Gi"}]}`)); err != nil {
		t.Fatalf("valid usage rejected: %v", err)
	}
	for name, v := range map[string]string{
		"payload key": `{"provider":"acme-db","accountId":"1","metrics":[{"name":"storage-used","value":31}]}`,
		"metric key":  `{"provider":"acme-db","metrics":[{"name":"storage-used","value":31,"labels":{}}]}`,
		"cost key":    `{"provider":"acme-db","cost":{"amount":"1","currency":"USD","period":"P30D","tax":"0"}}`,
	} {
		if err := decodeUsage(usageJSON("Observed", v)); err == nil {
			t.Errorf("%s: an unknown key was accepted", name)
		}
	}
}

func TestUsageNeedsMetricsOrCost(t *testing.T) {
	for name, v := range map[string]string{
		"neither":        `{"provider":"acme-db"}`,
		"empty metrics":  `{"provider":"acme-db","metrics":[]}`,
		"no provider":    `{"metrics":[{"name":"storage-used","value":31}]}`,
		"metric value":   `{"provider":"acme-db","metrics":[{"name":"storage-used","unit":"Gi"}]}`,
		"null value":     `{"provider":"acme-db","metrics":[{"name":"storage-used","value":null}]}`,
		"metrics object": `{"provider":"acme-db","metrics":{"name":"storage-used","value":1}}`,
	} {
		if err := decodeUsage(usageJSON("Observed", v)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, v := range map[string]string{
		"metrics only": `{"provider":"acme-db","metrics":[{"name":"storage-used","value":0}]}`,
		"cost only":    `{"provider":"acme-db","cost":{"amount":"212.40","currency":"USD","period":"P30D"}}`,
	} {
		if err := decodeUsage(usageJSON("Observed", v)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUsageBounds(t *testing.T) {
	metric := func(name string, v float64, unit string) UsageMetric {
		return UsageMetric{Name: name, Value: v, Unit: unit}
	}
	cost := func(amount, cur, period string) *UsageCost {
		return &UsageCost{Amount: amount, Currency: cur, Period: period}
	}
	many := make([]UsageMetric, 17)
	for i := range many {
		many[i] = metric("m"+strings.Repeat("x", i), 1, "")
	}
	cases := []struct {
		name string
		p    UsageObservation
		ok   bool
	}{
		{"valid", UsageObservation{Provider: "acme-db", Environment: "production", ExternalRef: "orders-db", Metrics: []UsageMetric{metric("storage-used", 31, "Gi")}, Cost: cost("212.40", "USD", "P30D")}, true},
		{"16 metrics", UsageObservation{Provider: "acme-db", Metrics: many[:16]}, true},
		{"17 metrics", UsageObservation{Provider: "acme-db", Metrics: many}, false},
		{"NaN", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", math.NaN(), "")}}, false},
		{"infinite", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", math.Inf(1), "")}}, false},
		{"huge", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1e308, "")}}, false},
		{"negative", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", -1, "")}}, false},
		{"duplicate metric", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1, ""), metric("m", 2, "")}}, false},
		{"metric name not a slug", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("Storage Used", 1, "")}}, false},
		{"unit 16 bytes", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1, strings.Repeat("u", 16))}}, true},
		{"unit 17 bytes", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1, strings.Repeat("u", 17))}}, false},
		{"unit not printable", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1, "G\ni")}}, false},
		{"unit not ASCII", UsageObservation{Provider: "acme-db", Metrics: []UsageMetric{metric("m", 1, "µs")}}, false},
		{"bad currency", UsageObservation{Provider: "acme-db", Cost: cost("1", "usd", "P30D")}, false},
		{"unicode currency", UsageObservation{Provider: "acme-db", Cost: cost("1", "ÜSD", "P30D")}, false},
		{"bad amount", UsageObservation{Provider: "acme-db", Cost: cost("1,5", "USD", "P30D")}, false},
		{"amount too precise", UsageObservation{Provider: "acme-db", Cost: cost("1.1234567", "USD", "P30D")}, false},
		{"amount too large", UsageObservation{Provider: "acme-db", Cost: cost(strings.Repeat("9", 16), "USD", "P30D")}, false},
		{"bad period", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "30 days")}, false},
		{"period P2Y", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P2Y")}, false},
		{"period P1M15D", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P1M15D")}, false},
		{"period P0D", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P0D")}, false},
		{"period P366D", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P366D")}, false},
		{"period P13M", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P13M")}, false},
		{"period P1D", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P1D")}, true},
		{"period P1W", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P1W")}, true},
		{"period P12M", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P12M")}, true},
		{"period P1Y", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P1Y")}, true},
		{"period overflow", UsageObservation{Provider: "acme-db", Cost: cost("1", "USD", "P99999999999999999999D")}, false},
		{"credential URL", UsageObservation{Provider: "acme-db", ExternalRef: "postgres://u:p@db/x", Cost: cost("1", "USD", "P1D")}, false},
		{"254-byte ref", UsageObservation{Provider: "acme-db", ExternalRef: strings.Repeat("x", 254), Cost: cost("1", "USD", "P1D")}, false},
		{"ref control char", UsageObservation{Provider: "acme-db", ExternalRef: "x\ny", Cost: cost("1", "USD", "P1D")}, false},
		{"provider not a slug", UsageObservation{Provider: "Acme", Cost: cost("1", "USD", "P1D")}, false},
		{"environment not a slug", UsageObservation{Provider: "acme-db", Environment: "Prod", Cost: cost("1", "USD", "P1D")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateUsage(tc.p); (err == nil) != tc.ok {
				t.Errorf("ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

func TestResourceUsageObservation(t *testing.T) {
	p := UsageObservation{Provider: "acme-db", Environment: "production", Metrics: []UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}}}
	o := NewResourceUsage(sr("dependency", "postgresql"), p, prov())
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var back Observation
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	got, err := back.GetUsageObservation()
	if err != nil || got.Metrics[0].Value != 31 || back.UsageEnvironment() != "production" {
		t.Errorf("round trip: %+v %v", got, err)
	}
	if _, err := back.GetProvisionedObservation(); err == nil {
		t.Error("a usage observation is not a provisioning one")
	}
	u, err := NewUnobserved(ResourceUsage, SubjectRef{Kind: "configuration", Name: "db", Environment: "staging"}, Failed, prov())
	if err != nil || u.UsageEnvironment() != "staging" {
		t.Errorf("unobserved usage: %+v %v", u, err)
	}
	if _, err := NewUnobserved(ResourceUsage, sr("interface", "api"), Failed, prov()); err == nil {
		t.Error("usage names a configuration or dependency")
	}
	if !ResourceUsage.Known() || !ResourceUsage.ProviderReported() || !ResourceProvisioned.ProviderReported() || WorkloadObserved.ProviderReported() {
		t.Error("ResourceUsage and ResourceProvisioned are the provider-reported kinds")
	}
}

// TestAttackHostileUsagePayloads: hostile usage payloads on the wire are
// refused at decode, before any store or fleet sees them.
func TestAttackHostileUsagePayloads(t *testing.T) {
	metric := `{"name":"m","value":1}`
	many := make([]string, 10000)
	for i := range many {
		many[i] = `{"name":"m` + strings.Repeat("x", i%40) + `","value":1}`
	}
	for name, v := range map[string]string{
		"10,000 metrics":       `{"provider":"acme-db","metrics":[` + strings.Join(many, ",") + `]}`,
		"NaN value":            `{"provider":"acme-db","metrics":[{"name":"m","value":NaN}]}`,
		"infinite value":       `{"provider":"acme-db","metrics":[{"name":"m","value":1e400}]}`,
		"1e308 value":          `{"provider":"acme-db","metrics":[{"name":"m","value":1e308}]}`,
		"1 MB unit":            `{"provider":"acme-db","metrics":[{"name":"m","value":1,"unit":"` + strings.Repeat("u", 1<<20) + `"}]}`,
		"lowercase currency":   `{"provider":"acme-db","cost":{"amount":"1","currency":"usd","period":"P30D"}}`,
		"unicode currency":     `{"provider":"acme-db","cost":{"amount":"1","currency":"ＵＳＤ","period":"P30D"}}`,
		"credential URL":       `{"provider":"acme-db","externalRef":"https://user:pass@db.example/x","metrics":[` + metric + `]}`,
		"unknown key":          `{"provider":"acme-db","metrics":[` + metric + `],"owner":"team-b"}`,
		"string value":         `{"provider":"acme-db","metrics":[{"name":"m","value":"1"}]}`,
		"negative cost amount": `{"provider":"acme-db","cost":{"amount":"-1","currency":"USD","period":"P30D"}}`,
	} {
		if err := decodeUsage(usageJSON("Observed", v)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
