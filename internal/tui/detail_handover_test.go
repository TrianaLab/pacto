package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The TUI prints a handed-over service's replacement and fields in the
// CLI's words (TestPrintFleetServiceHandover), from the same P4 fixture.
func TestRenderServiceDetailFields(t *testing.T) {
	b, err := os.ReadFile("../../pkg/fleet/testdata/handover.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Service struct {
			Replaces      []fleet.Replacement
			ReplacesTotal int
		}
		DetailFields *fleet.FieldsPreview
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	renderServiceDetail(&out, "invoices", &fleet.ServiceDetailData{Replaces: g.Service.Replaces, ReplacesTotal: g.Service.ReplacesTotal + 1,
		ReplacesHidden: 2, Fields: g.DetailFields}, fleet.CompletenessComplete, time.Time{})
	want := []string{
		"Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:invoices-0.1.0)",
		"  Workload: Deployment 9f1c4e2a-0000-4000-8000-00000000000a billing/invoices",
		"  1 more",
		"  2 whose workload you cannot see",
		"  owner         billing    declared",
		"  ledger range  ^2.3.0     accepted by ana@acme.com",
		"  audit range   unknown    proposed ^1.4.0 (fleet, partial)",
		"  workload      service    inferred from deploy/app.yaml:1",
	}
	got := out.String()
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w+"\n")
		if i < 0 {
			t.Fatalf("detail lacks %q after offset %d:\n%s", w, at, got)
		}
		at += i + len(w)
	}
	var plain strings.Builder
	renderServiceDetail(&plain, "web", &fleet.ServiceDetailData{}, fleet.CompletenessComplete, time.Time{})
	if strings.Contains(plain.String(), "Replaces") || strings.Contains(plain.String(), "Fields") {
		t.Errorf("a service with no replacement or field provenance renders them:\n%s", plain.String())
	}
}

// A replacement no source reported says so, never nothing.
func TestRenderServiceDetailReplacementWithoutWorkload(t *testing.T) {
	r := fleet.Replacement{From: fleet.NewServiceKey("k8s:billing/Deployment/invoices"), To: "invoices", Link: "inferred-from", By: "ana@acme.com", Revision: "invoices@sha256:a"}
	var out strings.Builder
	renderServiceDetail(&out, "invoices", &fleet.ServiceDetailData{Replaces: []fleet.Replacement{r}, ReplacesTotal: 1}, fleet.CompletenessComplete, time.Time{})
	if !strings.Contains(out.String(), "(revision invoices@sha256:a); Pacto read no workload for it here\n") || strings.Contains(out.String(), "Workload:") {
		t.Errorf("detail:\n%s", out.String())
	}
}
