/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"os/exec"
	"strings"
	"testing"
)

// chartDir is the packaged chart this operator is released with.
const chartDir = "../../charts/pacto-operator"

// argPrefix is how a rendered trace-source flag appears in the Deployment spec.
const argPrefix = "- --dashboard-trace-source="

// renderTraceSourceArgs renders the chart with one observation source and returns
// the trace-source flag VALUES the controller would be started with, plus the
// render error. Rendering runs Helm's own values.schema.json validation first, so
// a rejected value never reaches the template.
func renderTraceSourceArgs(t *testing.T, sourceJSON string) ([]string, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		// Both CI legs that exercise the chart install Helm; a workstation without
		// it still runs every other test in this file.
		t.Skip("helm not on PATH")
	}
	out, err := exec.Command(helm, "template", "pacto-operator", chartDir,
		"--set-json", "dashboard.observation.sources=["+sourceJSON+"]").CombinedOutput()
	if err != nil {
		return nil, err
	}
	var args []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, argPrefix) {
			args = append(args, strings.TrimPrefix(trimmed, argPrefix))
		}
	}
	return args, nil
}

// TestChartValuesRoundTripThroughTheFlagWire is the agreement proof between the
// public configuration contract and the controller's wire: a value the chart
// accepts renders to a flag the controller parses back into the SAME source. The
// round trip runs through the real chart rather than a restated grammar, so the
// two cannot drift apart without this failing.
func TestChartValuesRoundTripThroughTheFlagWire(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceJSON string
		want       ObservationSource
	}{
		{
			name:       "pvc backing",
			sourceJSON: `{"name":"orders","file":"traces.json","existingClaim":"orders-trace-export"}`,
			want:       ObservationSource{Name: "orders", File: "traces.json", ExistingClaim: "orders-trace-export"},
		},
		{
			name:       "configmap backing",
			sourceJSON: `{"name":"fixture","file":"traces.json","configMap":"fixture-traces"}`,
			want:       ObservationSource{Name: "fixture", File: "traces.json", ConfigMap: "fixture-traces"},
		},
		{
			// Only the FIRST "=" of a field separates key from value, so a file name
			// may contain one. The wire is restricted at the comma, nowhere else.
			name:       "equals sign in the file name",
			sourceJSON: `{"name":"orders","file":"trace=export.json","existingClaim":"orders-trace-export"}`,
			want:       ObservationSource{Name: "orders", File: "trace=export.json", ExistingClaim: "orders-trace-export"},
		},
		{
			name:       "dotted kubernetes object name",
			sourceJSON: `{"name":"orders","file":"traces.json","configMap":"traces.orders.example"}`,
			want:       ObservationSource{Name: "orders", File: "traces.json", ConfigMap: "traces.orders.example"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := renderTraceSourceArgs(t, tc.sourceJSON)
			if err != nil {
				t.Fatalf("helm template rejected a legal source: %v", err)
			}
			if len(args) != 1 {
				t.Fatalf("rendered trace-source args = %q, want exactly 1", args)
			}
			got, err := ParseObservationSource(args[0])
			if err != nil {
				t.Fatalf("the controller cannot parse its own rendered flag %q: %v", args[0], err)
			}
			if got != tc.want {
				t.Errorf("round trip = %+v, want %+v (rendered %q)", got, tc.want, args[0])
			}
		})
	}
}

// TestChartRejectsWhatTheWireCannotCarry pins the other half of the agreement: a
// value the controller could not parse (or would parse into something else) never
// renders at all. Each case asserts BOTH ends reject it, so neither can be relaxed
// alone.
func TestChartRejectsWhatTheWireCannotCarry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceJSON string
		source     ObservationSource
	}{
		{
			// The counterexample: legal under the previous schema, and the flag wire
			// read "part.json" as a separate malformed field.
			name:       "comma in the file name",
			sourceJSON: `{"name":"orders","file":"trace,part.json","existingClaim":"orders-trace-export"}`,
			source:     ObservationSource{Name: "orders", File: "trace,part.json", ExistingClaim: "orders-trace-export"},
		},
		{
			name:       "nested file path",
			sourceJSON: `{"name":"orders","file":"exports/traces.json","existingClaim":"orders-trace-export"}`,
			source:     ObservationSource{Name: "orders", File: "exports/traces.json", ExistingClaim: "orders-trace-export"},
		},
		{
			name:       "claim name is not a kubernetes object name",
			sourceJSON: `{"name":"orders","file":"traces.json","existingClaim":"Orders_Trace_Export"}`,
			source:     ObservationSource{Name: "orders", File: "traces.json", ExistingClaim: "Orders_Trace_Export"},
		},
		{
			name:       "configMap name is not a kubernetes object name",
			sourceJSON: `{"name":"orders","file":"traces.json","configMap":"orders traces"}`,
			source:     ObservationSource{Name: "orders", File: "traces.json", ConfigMap: "orders traces"},
		},
		{
			name:       "escaping file name",
			sourceJSON: `{"name":"orders","file":"../traces.json","existingClaim":"orders-trace-export"}`,
			source:     ObservationSource{Name: "orders", File: "../traces.json", ExistingClaim: "orders-trace-export"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.source.Validate(); err == nil {
				t.Error("the controller accepted a source the chart must not be able to express")
			}
			if _, err := renderTraceSourceArgs(t, tc.sourceJSON); err == nil {
				t.Error("helm template accepted a value the controller rejects")
			}
		})
	}
}
