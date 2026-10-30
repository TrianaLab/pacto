package contract

import (
	"reflect"
	"strings"
	"testing"
)

func TestUnmet(t *testing.T) {
	provider := &Contract{
		Service: Service{Name: "orders"},
		Interfaces: []Interface{
			{Name: "events", Guarantees: &Guarantees{Delivery: "at-least-once", Ordering: OrderingPerKey, DedupeWindow: "1m", Retention: "168h"}},
			{Name: "api", Guarantees: &Guarantees{Idempotent: true, Timeout: "2s"}},
			{Name: "bare"},
		},
	}
	dep := func(r ...Requirement) Dependency { return Dependency{Name: "orders", Requires: r} }
	req := func(iface string, g Guarantees) Requirement { return Requirement{Interface: iface, Guarantees: g} }

	tests := []struct {
		name string
		dep  Dependency
		want []string // guarantee=required/offered
	}{
		{"no requirements", dep(), nil},
		{"all met", dep(
			req("events", Guarantees{Profile: "1", Delivery: "at-least-once", Ordering: OrderingPerKey, DedupeWindow: "30s", Retention: "24h"}),
			req("api", Guarantees{Idempotent: true, Timeout: "5s"}),
			req("api", Guarantees{Timeout: "2s"}),
		), nil},
		{"every mismatch at once", dep(
			req("events", Guarantees{Delivery: "at-most-once", Ordering: OrderingGlobal, DedupeWindow: "5m", Retention: "720h"}),
			req("api", Guarantees{Timeout: "1s"}),
		), []string{
			"delivery=at-most-once/at-least-once", "ordering=global/per-key", "dedupeWindow=5m/1m",
			"retention=720h/168h", "timeout=1s/2s",
		}},
		{"undeclared guarantees", dep(req("bare", Guarantees{Idempotent: true, Timeout: "1s", Delivery: "at-least-once"})),
			[]string{"delivery=at-least-once/", "timeout=1s/", "idempotent=true/"}},
		{"missing interface", dep(req("gone", Guarantees{Idempotent: true})), []string{"interface=gone/"}},
		{"unreadable duration meets nothing", dep(req("events", Guarantees{DedupeWindow: "five minutes"})), []string{"dedupeWindow=five minutes/1m"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, m := range Unmet("checkout", tt.dep, provider) {
				if m.Consumer != "checkout" || m.Provider != "orders" {
					t.Errorf("mismatch names %s -> %s", m.Consumer, m.Provider)
				}
				got = append(got, m.Guarantee+"="+m.Required+"/"+m.Offered)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMismatchString(t *testing.T) {
	for _, tt := range []struct {
		m    Mismatch
		want string
	}{
		{Mismatch{Consumer: "checkout", Provider: "orders", Interface: "events", Guarantee: "dedupeWindow", Required: "5m", Offered: "1m"},
			"checkout requires events dedupeWindow >= 5m from orders, which offers 1m"},
		{Mismatch{Consumer: "checkout", Provider: "orders", Interface: "api", Guarantee: "idempotent", Required: "true"},
			"checkout requires api idempotent true from orders, which offers nothing"},
		{Mismatch{Consumer: "checkout", Provider: "orders", Interface: "gone", Guarantee: "interface", Required: "gone"},
			"checkout requires interface gone from orders, which declares no such interface"},
	} {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestRequirementParses(t *testing.T) {
	c, err := Parse(strings.NewReader(`pactoVersion: "2.2"
service: {name: checkout, version: 1.0.0}
interfaces:
  - {name: api, type: openapi, ref: api.yaml, guarantees: {idempotent: true, timeout: 2s}}
dependencies:
  - name: orders
    ref: oci://ghcr.io/acme/orders
    required: true
    compatibility: ^1.0.0
    requires:
      - {interface: events, delivery: at-least-once, dedupeWindow: 5m}
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Requirement{Interface: "events", Guarantees: Guarantees{Delivery: "at-least-once", DedupeWindow: "5m"}}
	if got := c.Dependencies[0].Requires; len(got) != 1 || got[0] != want {
		t.Errorf("requires = %+v", got)
	}
	if g := c.Interfaces[0].Guarantees; g == nil || !g.Idempotent || g.Timeout != "2s" {
		t.Errorf("guarantees = %+v", g)
	}
}

func TestGuaranteesTermsAndText(t *testing.T) {
	g := Guarantees{Profile: "1", Delivery: "at-least-once", Ordering: "per-key", DedupeWindow: "10m", Retention: "168h", Timeout: "5s", Idempotent: true}
	var names []string
	for _, term := range g.Terms() {
		names = append(names, term.Name+"="+term.Value+"/"+term.Bound)
	}
	want := "delivery=at-least-once/ ordering=per-key/ dedupe window=10m/at least retention=168h/at least timeout=5s/at most idempotent=true/"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("Terms = %q, want %q", got, want)
	}
	if got := g.Text(false); got != "delivery at-least-once, ordering per-key, dedupe window 10m, retention 168h, timeout 5s, idempotent" {
		t.Errorf("Text(false) = %q", got)
	}
	r := Requirement{Interface: "http", Guarantees: Guarantees{Timeout: "5s", Idempotent: true}}
	if got := r.Text(true); got != "timeout at most 5s, idempotent" {
		t.Errorf("Text(true) = %q", got)
	}
	if got := (Guarantees{}).Text(false); got != "" {
		t.Errorf("empty Text = %q", got)
	}
}
