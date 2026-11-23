package cli_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestImpactChangedExternalExits2: a change of an external system is never
// verified, so the command exits 2 with every consumer in attention.
func TestImpactChangedExternalExits2(t *testing.T) {
	out, stderr, err := execFleet(t, "impact", "--changed", "external:stripe", "--local", writeExternalFixture(t))
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("changed external = %v, want exit 2\n%s", err, out)
	}
	for _, want := range []string{"Event: stripe changed (external, vendor-api): 2 consumers, 2 declared, 0 observed only", "ATTENTION (2):",
		"  checkout ", "  pay-agent ", "declare it:\n  dependencies:\n", "Release gate: unverified"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr, "unverified: "+impact.GateReasonExternal) {
		t.Errorf("stderr lacks the gate reason:\n%s", stderr)
	}
}

func TestImpactChangedExternalJSON(t *testing.T) {
	out, _, err := execFleet(t, "impact", "--changed", "external:stripe", "--local", writeExternalFixture(t), "--output-format", "json")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("changed external json = %v, want exit 2", err)
	}
	var res impact.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.Gate != impact.GateUnverified || res.External == nil || res.External.Identity != "stripe" || res.Pin == "" || len(res.Consumers) != 2 {
		t.Fatalf("json answer = %+v", res)
	}
	for _, c := range res.Consumers {
		if c.Effect != impact.EffectAttention || len(c.Sources) == 0 {
			t.Errorf("consumer %s = %+v", c.Service, c)
		}
	}
}

// TestImpactUnavailableExternal: an outage breaks every declared required
// use; the gate stays unverified.
func TestImpactUnavailableExternal(t *testing.T) {
	out, _, err := execFleet(t, "impact", "--unavailable", "external:stripe", "--local", writeExternalFixture(t))
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("unavailable external = %v, want exit 2\n%s", err, out)
	}
	for _, want := range []string{"Event: stripe unavailable (external, vendor-api)", "BREAKS (2):", "unknown  breaks\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
}

// TestImpactChangedRequiresExternal: --changed asks about an external system
// only, and an identity Pacto would rewrite is refused.
func TestImpactChangedRequiresExternal(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"checkout", "--changed takes external:<identity>"},
		{"external:Stripe", "pacto fleet search"},
	} {
		_, _, err := execFleet(t, "impact", "--changed", tc.value, "--local", writeExternalFixture(t))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--changed %s = %v, want %q", tc.value, err, tc.want)
		}
	}
	if _, _, err := execFleet(t, "impact", "--changed", "external:stripe", "--removed", "checkout"); err == nil || !strings.Contains(err.Error(), "not several") {
		t.Errorf("two event flags = %v", err)
	}
}
