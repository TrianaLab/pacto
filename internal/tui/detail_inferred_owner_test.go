package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestOwnerProvenanceMarkInferred(t *testing.T) {
	f := &fleet.InferredFact{Value: "team:team-ledger", Source: "catalog-a"}
	for _, c := range []struct {
		p, source string
		fact      *fleet.InferredFact
		want      string
	}{
		{fleet.OwnerProvenanceInferred, "", f, " (inferred: team-ledger from catalog-a)"},
		{fleet.OwnerProvenanceInferred, "", &fleet.InferredFact{Value: "odd", Source: "catalog-a"}, " (inferred: odd from catalog-a)"},
		{fleet.OwnerProvenanceInferred, "", nil, " (inferred)"},
		{fleet.OwnerProvenanceDeclared, "", f, ""},
		{fleet.OwnerProvenanceAttested, "CODEOWNERS:1", nil, " (attested: CODEOWNERS:1)"},
		{fleet.OwnerProvenanceDeclaredViaTarget, "", nil, " (declared via target)"},
	} {
		if got := OwnerProvenanceMark(c.p, c.source, c.fact); got != c.want {
			t.Errorf("%s: %q, want %q", c.p, got, c.want)
		}
	}
	var out strings.Builder
	renderServiceDetail(&out, "ledger", &fleet.ServiceDetailData{Ownership: &fleet.OwnershipInfo{Provenance: fleet.OwnerProvenanceInferred, Inferred: f}},
		fleet.CompletenessComplete, time.Time{})
	if !strings.Contains(out.String(), "(inferred: team-ledger from catalog-a)") {
		t.Errorf("service page:\n%s", out.String())
	}
}
