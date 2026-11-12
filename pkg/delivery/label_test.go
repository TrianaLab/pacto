package delivery

import "testing"

func TestVerdictLabel(t *testing.T) {
	for _, c := range []struct {
		v    Verdict
		want string
	}{
		{Verdict{Gate: GateImpact, Result: Pass}, "Checked"},
		{Verdict{Gate: GateDiff, Result: Pass}, "Checked"},
		{Verdict{Gate: GateEnvironment, Result: Pass}, "Checked"},
		{Verdict{Gate: GateContract, Result: Pass}, "Pass"},
		{Verdict{Gate: GateImpact, Result: Unknown}, "Unknown"},
		{Verdict{Gate: GateImpact, Result: Fail}, "Fail"},
		{Verdict{Gate: GateContract, Result: Pass, Citation: map[string]string{"applicable": "false"}}, "N/A"},
	} {
		if got := VerdictLabel(c.v); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.v.Gate, c.v.Result, got, c.want)
		}
	}
}
