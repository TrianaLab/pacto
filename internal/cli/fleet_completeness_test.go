package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestFleetStatusPrintsContractCompleteness(t *testing.T) {
	out := statusText(t, &fleet.StatusResult{ContractCompleteness: fleet.FleetCompleteness{
		PercentComplete: new(72), Judged: 2950, Unknown: 50, FullyDescribed: 1204,
		MostMissing: []fleet.SectionCount{{Section: "configuration", Count: 812}, {Section: "readiness", Count: 640}},
	}})
	want := "Contract completeness: 72% · 1204 of 2950 judged services fully described · 50 not judged · most missing: configuration 812, readiness 640\n"
	expect(t, strings.Contains(out, want), "status text lacks %q:\n%s", want, out)

	out = statusText(t, &fleet.StatusResult{ContractCompleteness: fleet.FleetCompleteness{Unknown: 3}})
	expect(t, strings.Contains(out, "Contract completeness: unknown · 0 of 0 judged services fully described · 3 not judged\n"), "nothing judged:\n%s", out)

	out = statusText(t, &fleet.StatusResult{})
	expect(t, !strings.Contains(out, "Contract completeness"), "no service prints no line:\n%s", out)
}
