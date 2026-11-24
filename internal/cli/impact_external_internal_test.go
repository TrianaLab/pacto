package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// externalChangedResult is a changed answer over stripe's inferred contract:
// payments declares it and is also observed calling it, billing declares it
// with no client major, and checkout is only observed.
func externalChangedResult() *impact.Result {
	return &impact.Result{
		AsOf: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), Completeness: fleet.CompletenessComplete, Gate: impact.GateUnverified,
		Event: &impact.Event{Kind: impact.EventChanged, External: "stripe"},
		External: &impact.ExternalSubject{Identity: "stripe", Class: "vendor-api", Provider: "registry.example.com/acme/pacto-inferred/stripe",
			Change: &fleet.ExternalProviderChange{From: "0.0.4", To: "0.0.5", Checked: true, Added: []string{"clients stripe-go/77"},
				OpenAPIFrom: "https://docs.stripe.com/openapi.json@sha256:3e9a000000000000000000000000000000000000000000000000000000000000",
				OpenAPITo:   "https://docs.stripe.com/openapi.json@sha256:7c21000000000000000000000000000000000000000000000000000000000000"}},
		Consumers: []impact.AffectedConsumer{
			{Service: "payments", Effect: impact.EffectAttention, Sources: []string{"declared", "observed"}, Compatibility: "^2023.10",
				CompatibilityVerdict: impact.CompatibilityUnknown, Client: &fleet.ExternalClient{SDK: "stripe-go", Major: 76, Confidence: "possible"}},
			{Service: "billing", Effect: impact.EffectAttention, Sources: []string{"declared"}, CompatibilityVerdict: impact.CompatibilityUnknown},
			{Service: "checkout", Effect: impact.EffectAttention, Sources: []string{"observed"}, CompatibilityVerdict: impact.CompatibilityUnknown},
		},
		Pin: "dependencies:\n  - name: stripe\n    ref: oci://registry.example.com/acme/pacto-inferred/stripe\n    required: true\n    compatibility: \"^2023.10\"\n",
	}
}

func TestImpactChangedExternalText(t *testing.T) {
	got := textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, externalChangedResult(), "text") })
	want := "Event: stripe changed (inferred external, vendor-api): 3 consumers, 2 declared, 1 observed only  asOf 2026-11-22T00:00:00Z  completeness complete\n" +
		"changed: 0.0.4 -> 0.0.5  openapi sha256:3e9a00000000... -> sha256:7c2100000000...  clients +stripe-go/77\n" +
		"gate: unverified (provider is inferred)\n" +
		"Affected consumers (3):\n" +
		"ATTENTION (3):\n" +
		"  CONSUMER  SOURCE              CLIENT MAJOR              RANGE                                  OUTCOME\n" +
		"  payments  declared, observed  stripe-go v76 (possible)  ^2023.10 (unknown: INFERRED_PROVIDER)  attention\n" +
		"  billing   declared            -                         unknown                                attention\n" +
		"  checkout  observed            -                         unknown                                attention\n" +
		"pin a range:\n" +
		"  dependencies:\n" +
		"    - name: stripe\n" +
		"      ref: oci://registry.example.com/acme/pacto-inferred/stripe\n" +
		"      required: true\n" +
		"      compatibility: \"^2023.10\"\n" +
		"Release gate: unverified\n"
	if got != want {
		t.Errorf("external changed text:\n%s\nwant:\n%s", got, want)
	}
}

// TestImpactExternalTextWithoutInferredContract: with no inferred contract
// the header says external, an unchecked change says so, a link added reads
// from none, a range keeps its own verdict and the pin declares the system.
func TestImpactExternalTextWithoutInferredContract(t *testing.T) {
	res := externalChangedResult()
	res.Event.Kind = impact.EventUnavailable
	res.External.Provider, res.External.Class = "", ""
	res.External.Change = &fleet.ExternalProviderChange{From: "0.0.1", To: "0.0.2", Removed: []string{"clients stripe-go/75"}, OpenAPITo: "oci://r/s@sha256:ab"}
	res.Consumers = []impact.AffectedConsumer{{Service: "payments", Effect: impact.EffectBreaks, Sources: []string{"pod-spec"}, Compatibility: "^1", CompatibilityVerdict: "unknown"}}
	res.Pin = "dependencies:\n  - name: stripe\n"
	got := textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	wantLines(t, got,
		"Event: stripe unavailable (external): 1 consumer, 0 declared, 0 observed only, 1 inferred only  asOf",
		"changed: 0.0.1 -> 0.0.2  openapi none -> sha256:ab  clients -stripe-go/75  (unchecked: this installation's operator did not sign both revisions)\n",
		"gate: unverified (an external system is never verified)\n",
		"BREAKS (1):\n", "  payments  pod-spec  -             ^1 (unknown)  breaks\n",
		"declare it:\n  dependencies:\n    - name: stripe\n",
	)
}

// TestImpactExternalTextNoPin: every consumer pins a range, so nothing is
// suggested.
func TestImpactExternalTextNoPin(t *testing.T) {
	res := externalChangedResult()
	res.Pin = ""
	got := textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	if strings.Contains(got, "pin a range:") || strings.Contains(got, "declare it:") {
		t.Errorf("no pin still prints one:\n%s", got)
	}
}

func TestImpactChangedExternalPrintsEngine(t *testing.T) {
	res := externalChangedResult()
	res.External.Change = &fleet.ExternalProviderChange{From: "0.0.4", To: "0.0.5", Checked: true, EngineFrom: "postgres 15", EngineTo: "postgres 16"}
	got := textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	wantLines(t, got, "changed: 0.0.4 -> 0.0.5  engine postgres 15 -> postgres 16\n")
	res.External.Change.EngineFrom = ""
	got = textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	wantLines(t, got, "changed: 0.0.4 -> 0.0.5  engine none -> postgres 16\n")
}
