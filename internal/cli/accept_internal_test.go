package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// acceptPlanFixture accepts ledger's proposed range, a range of audit other
// than the proposal and ledger's proposed ref, with one acceptance attached.
func acceptPlanFixture() *app.AcceptPlan {
	return &app.AcceptPlan{Service: "reports", Version: "1.6.0", Subject: "ghcr.io/acme/reports@sha256:ab", Kind: delivery.AcceptKindRanges,
		Ranges: []delivery.AcceptedRange{
			{Field: "dependencies[audit].compatibility", Value: "^1.5.0", Proposed: "^1.4.0", Source: "snapshot:s"},
			{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Proposed: "^2.3.0", Source: "snapshot:s"},
			{Field: "dependencies[ledger].ref", Value: "oci://ghcr.io/acme/ledger", Source: "index:i:t"},
		},
		WhoMayAccept: "the owner of reports: ana@acme.com",
		Existing: []impact.Acceptance{{State: impact.AcceptRouted, By: "bob@acme.com", Record: "ghcr.io/acme/reports@sha256:cd", Reason: "waiting for the owner of reports: ana@acme.com"},
			{State: impact.AcceptCounted, By: "ana@acme.com", Record: "ghcr.io/acme/reports@sha256:ef"}},
		Notes:        []string{"no installation signer policy was read; acceptances judged with public-good issuers"},
		IntentDigest: declareIntent}
}

// acceptDeps answers plans with plan (or err) and keeps the options.
func acceptDeps(got *app.AcceptOptions, plan *app.AcceptPlan, err error) deps {
	d := defaultDeps()
	d.planAccept = func(_ *app.Service, _ context.Context, opts app.AcceptOptions) (*app.AcceptPlan, error) {
		*got = opts
		return plan, err
	}
	return d
}

const acceptText = `plan: accept for reports 1.6.0 (ghcr.io/acme/reports@sha256:ab)
  dependencies[audit].compatibility: ^1.5.0 (proposed ^1.4.0 from snapshot:s)
  dependencies[ledger].compatibility: ^2.3.0 (from snapshot:s)
  dependencies[ledger].ref: oci://ghcr.io/acme/ledger (from index:i:t; this bundle repository becomes declared)
who may accept: the owner of reports: ana@acme.com
attached: routed by bob@acme.com (ghcr.io/acme/reports@sha256:cd): waiting for the owner of reports: ana@acme.com
attached: counted by ana@acme.com (ghcr.io/acme/reports@sha256:ef)
note: no installation signer policy was read; acceptances judged with public-good issuers
intent: ` + declareIntent + `
nothing was written: this is a dry run
`

func TestAcceptDryRunPrintsPlan(t *testing.T) {
	var got app.AcceptOptions
	out, _, err := runRoot(t, acceptDeps(&got, acceptPlanFixture(), nil), nil,
		"accept", "oci://ghcr.io/acme/reports", "--proposed", "--field", "dependencies[ledger].ref", "dependencies[audit].compatibility=^1.5.0", "--dry-run")
	if err != nil || out != acceptText {
		t.Fatalf("err %v\n%s", err, out)
	}
	if got.Ref != "oci://ghcr.io/acme/reports" || !got.Proposed || strings.Join(got.Fields, " ") != "dependencies[ledger].ref dependencies[audit].compatibility=^1.5.0" {
		t.Fatalf("options = %+v", got)
	}
	// A folder is the service's checkout.
	dir := t.TempDir()
	if _, _, err := runRoot(t, acceptDeps(&got, acceptPlanFixture(), nil), nil, "accept", dir, "--proposed", "--dry-run"); err != nil || got.Dir != dir || got.Ref != "" {
		t.Fatalf("options = %+v %v", got, err)
	}
	// This release only plans.
	if _, _, err := runRoot(t, acceptDeps(&got, acceptPlanFixture(), nil), nil, "accept", dir, "--proposed"); err == nil || !strings.Contains(err.Error(), "dry-run") {
		t.Fatalf("a run without --dry-run: %v", err)
	}
}

func TestAcceptDryRunPrintsBreak(t *testing.T) {
	var got app.AcceptOptions
	p := &app.AcceptPlan{Service: "reports", Version: "1.6.0", Subject: "r@sha256:ab", Kind: delivery.AcceptKindBreak,
		Break:        &delivery.AcceptedBreak{Provider: "ledger", Version: "3.0.0", Announcement: "sha256:aa"},
		WhoMayAccept: "reports declares no owner", IntentDigest: declareIntent}
	out, _, err := runRoot(t, acceptDeps(&got, p, nil), nil, "accept", "r", "--break", "ledger@3.0.0", "--from", "../ledger", "--dry-run")
	if err != nil || !strings.Contains(out, "  break: ledger 3.0.0 (announcement sha256:aa, expiry unknown: run pacto ci or declare expires)\n") {
		t.Fatalf("err %v\n%s", err, out)
	}
	p.Expires = "2026-12-31"
	if out, _, _ = runRoot(t, acceptDeps(&got, p, nil), nil, "accept", "r", "--break", "ledger@3.0.0", "--dry-run"); !strings.Contains(out, "expires 2026-12-31)") {
		t.Fatalf("out %s", out)
	}
	if got.Break != "ledger@3.0.0" || got.From != "" {
		t.Fatalf("options = %+v", got)
	}
}

func TestAcceptRefusalPrintsCommand(t *testing.T) {
	var got app.AcceptOptions
	refusal := &app.DeclareError{Code: "PlanStale", Message: "the plan changed", CLI: "pacto accept r --proposed"}
	out, _, err := runRoot(t, acceptDeps(&got, acceptPlanFixture(), refusal), nil, "accept", "r", "--proposed", "--intent", "sha256:00", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "the plan changed\nrun: pacto accept r --proposed") || strings.Contains(out, "nothing was written") || got.Intent != "sha256:00" {
		t.Fatalf("err %v\n%s", err, out)
	}
	if _, _, err := runRoot(t, acceptDeps(&got, nil, errors.New("registry down")), nil, "accept", "r", "--proposed", "--dry-run"); err == nil || err.Error() != "registry down" {
		t.Fatalf("err %v", err)
	}
}

func TestAcceptJSONOneDocument(t *testing.T) {
	var got app.AcceptOptions
	out, _, err := runRoot(t, acceptDeps(&got, acceptPlanFixture(), nil), nil, "accept", "r", "--proposed", "--dry-run", "--output-format", "json")
	var doc map[string]any
	if err != nil || json.Unmarshal([]byte(out), &doc) != nil || doc["intentDigest"] != declareIntent || doc["refusal"] != nil {
		t.Fatalf("err %v\n%s", err, out)
	}
	refusal := &app.DeclareError{Code: "NotPublished", Message: "nothing published", CLI: "pacto ci --publish ."}
	out, _, err = runRoot(t, acceptDeps(&got, nil, refusal), nil, "accept", "r", "--proposed", "--dry-run", "--output-format", "json")
	doc = nil
	dec := json.NewDecoder(strings.NewReader(out))
	if err == nil || dec.Decode(&doc) != nil || dec.More() || doc["exit"] == nil || doc["refusal"].(map[string]any)["code"] != "NotPublished" {
		t.Fatalf("err %v\n%s", err, out)
	}
}
