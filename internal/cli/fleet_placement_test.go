package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func TestPrintFleetEntityEnvironments(t *testing.T) {
	p := fleet.PlacementPreview{Total: 3, Count: 2, Truncated: true, Items: []fleet.EnvironmentPlacement{
		{Environment: "prod", Targets: 2, Declared: 2, RevisionsTotal: 1, Revisions: []fleet.EntityRef{{Version: "1.2.0"}},
			Declaration: &fleet.EnvironmentResult{Status: fleet.EnvironmentPass, Lifetime: "persistent"},
			Owner:       fleet.EnvironmentOwner{Owner: "payments", Provenance: "inferred"}, TargetsComplete: true,
			Provisioning: fleet.PhaseTally{Ready: 2, Failed: 1}, ProvisioningTracked: true},
		{Environment: "staging", Owner: fleet.EnvironmentOwner{Provenance: "unknown", Reason: "not declared by the current revision"},
			ProvisioningReason: "the current revision does not declare staging, so no provider is asked about it there"},
	}, DifferencesTotal: 1, Differences: []validation.EnvironmentDifference{{Configuration: "app", Path: "replicas", Values: map[string]any{"prod": 3}}}}
	var b strings.Builder
	printPlacement(&b, "web", p, fleet.CompletenessComplete)
	want := "Environments:\n" +
		"  prod: declared: values pass, persistent; owner payments (inferred); targets you can see: 2 (2 declared), 1.2.0; provisioning 2 ready, 1 failed of 3\n" +
		"  staging: not declared; owner unknown: not declared by the current revision; targets you can see: 0 (incomplete); " +
		"provisioning not tracked: the current revision does not declare staging, so no provider is asked about it there\n" +
		"  1 more environments\n" +
		"Differences (1 of 1):\n  app.replicas:\n    prod  3\n" +
		"Stages: pacto delivery status --service web\n"
	if b.String() != want {
		t.Errorf("environments:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	printPlacement(&b, "web", p, fleet.CompletenessPartial)
	if !strings.HasPrefix(b.String(), "Environments (targets Pacto can see):\n") {
		t.Errorf("partial heading:\n%s", b.String())
	}
	b.Reset()
	printPlacement(&b, "web", fleet.PlacementPreview{}, fleet.CompletenessComplete)
	if b.Len() != 0 {
		t.Errorf("no environments prints nothing, got:\n%s", b.String())
	}
}

func TestPrintEnvironmentHistory(t *testing.T) {
	rev := fleet.EntityRef{Label: "web 1.0.0"}
	var b strings.Builder
	printEnvironmentHistory(&b, fleet.EnvironmentHistoryPreview{Total: 2, Count: 2, Completeness: "partial", CompletenessReason: "r", Items: []fleet.EnvironmentEvent{
		{Environment: "prod", Change: "added", Revision: fleet.EntityRef{Label: "web 1.1.0"}, Previous: &rev},
		{Environment: "dev", Change: "present", Revision: rev},
	}})
	want := "Environment history, version order (2 of 2, partial: r):\n" +
		"  web 1.1.0  added prod  time not recorded\n" +
		"  web 1.0.0  dev declared since at least web 1.0.0  time not recorded\n"
	if b.String() != want {
		t.Errorf("history:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	printEnvironmentHistory(&b, fleet.EnvironmentHistoryPreview{Gaps: 1})
	if b.String() != "Environment history, version order (0 of 0, 1 revision(s) unread):\n" {
		t.Errorf("gaps only:\n%s", b.String())
	}
	b.Reset()
	printEnvironmentHistory(&b, fleet.EnvironmentHistoryPreview{})
	if b.Len() != 0 {
		t.Errorf("nothing to say prints nothing, got:\n%s", b.String())
	}
}
