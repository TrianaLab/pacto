package impact

import "testing"

// TestGateCodeAgreesWithGate pins each row's (gate, code) pair, so GateCode
// never drifts from gate(): every code appears under exactly one verdict.
func TestGateCodeAgreesWithGate(t *testing.T) {
	for _, tt := range gateCases() {
		t.Run(tt.name, func(t *testing.T) {
			gate, _ := tt.r.gate()
			if gate != tt.gate || tt.r.GateCode() != tt.code {
				t.Errorf("gate %q code %q; want %q %q", gate, tt.r.GateCode(), tt.gate, tt.code)
			}
		})
	}
}

func TestGateCodeEmptyForEvents(t *testing.T) {
	r := Result{Classification: "BREAKING", Event: &Event{Kind: EventUnavailable, Service: "api"}}
	if got := r.GateCode(); got != "" {
		t.Fatalf("GateCode() = %q", got)
	}
}

func TestBlockingCountsBlockers(t *testing.T) {
	r := Result{Classification: "BREAKING", Consumers: []AffectedConsumer{
		{Service: "a", CompatibilityVerdict: CompatibilityIncompatible, Targets: []string{"t"}},
		{Service: "b", CompatibilityVerdict: CompatibilityIncompatible, Targets: []string{"t"}},
		{Service: "c", CompatibilityVerdict: CompatibilityIncompatible},
	}}
	if got := r.Blocking(); got != 2 {
		t.Fatalf("Blocking() = %d", got)
	}
}
