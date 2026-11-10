package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestPrintFleetEntityPlacement(t *testing.T) {
	p := fleet.PlacementPreview{Total: 3, Count: 2, Truncated: true, Items: []fleet.EnvironmentPlacement{
		{Environment: "prod", Targets: 2, Declared: 1, NotDeclared: 1, RevisionsTotal: 3, RevisionsTruncated: true,
			Revisions: []fleet.EntityRef{{Version: "1.2.0"}, {Label: "svc"}}},
		{Targets: 1, Unknown: 1, Revisions: []fleet.EntityRef{}},
	}}
	var b strings.Builder
	printPlacement(&b, p, fleet.CompletenessComplete)
	want := "Runs in (observed):\n" +
		"  prod: 2 targets, 1.2.0, svc and 1 more; 1 declared, 1 not declared, 0 unknown\n" +
		"  (no environment label): 1 target, no known revision; 0 declared, 0 not declared, 1 unknown\n" +
		"  1 more environments\n"
	if b.String() != want {
		t.Errorf("placement:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	printPlacement(&b, p, fleet.CompletenessPartial)
	if !strings.HasPrefix(b.String(), "Runs in (observed, targets Pacto can see):\n") {
		t.Errorf("partial heading:\n%s", b.String())
	}
	b.Reset()
	printPlacement(&b, fleet.PlacementPreview{}, fleet.CompletenessComplete)
	if b.Len() != 0 {
		t.Errorf("no targets prints nothing, got:\n%s", b.String())
	}
}
