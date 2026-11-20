package app

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func (e *acceptEnv) plan(t *testing.T, opts AcceptOptions) *AcceptPlan {
	t.Helper()
	p, err := e.svc.AcceptPlan(t.Context(), opts)
	if err != nil {
		t.Fatalf("%v\n%+v", err, p)
	}
	return p
}

// refusedAccept is the refusal code of planning opts.
func (e *acceptEnv) refusedAccept(t *testing.T, opts AcceptOptions) *DeclareError {
	t.Helper()
	_, err := e.svc.AcceptPlan(t.Context(), opts)
	var de *DeclareError
	if !errors.As(err, &de) || de.CLI == "" {
		t.Fatalf("err = %v", err)
	}
	return de
}

func planFields(p *AcceptPlan) []string {
	var out []string
	for _, r := range p.Ranges {
		out = append(out, r.Field+"="+r.Value)
	}
	return out
}

func TestAcceptPlanProposed(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Proposed = true
	p := e.plan(t, opts)
	want := []string{"dependencies[billing].compatibility=^1.4.0", "dependencies[ledger].compatibility=^2.3.0"}
	if strings.Join(planFields(p), " ") != strings.Join(want, " ") || p.Kind != delivery.AcceptKindRanges || p.Service != "reports" ||
		p.Version != "1.6.0" || p.Subject != e.subject || p.WhoMayAccept != "the owner of reports: ana@acme.com" || len(p.Notes) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	r := delivery.AcceptanceRecord{Service: "reports", Version: "1.6.0", Kind: delivery.AcceptKindRanges, Ranges: p.Ranges}
	if p.IntentDigest != delivery.AcceptanceIntent(r, e.subject) || p.Ranges[0].Source != snapSrc || p.Ranges[0].Proposed != "^1.4.0" {
		t.Fatalf("plan = %+v", p)
	}
	// The digest names the plan: the same one passes, another is stale.
	opts.Intent = p.IntentDigest
	e.plan(t, opts)
	opts.Intent, opts.Fields = "sha256:00", []string{"dependencies[ledger].compatibility"}
	if de := e.refusedAccept(t, opts); de.Code != codePlanStale || !strings.Contains(de.CLI, "--proposed --field 'dependencies[ledger].compatibility'") {
		t.Fatalf("refusal = %+v", de)
	}
}

func TestAcceptPlanListsAttachedAcceptances(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: rangesRecord(), token: personTok("bob@acme.com"), tag: true})
	opts := e.trust()
	opts.Proposed = true
	p := e.plan(t, opts)
	if len(p.Existing) != 1 || p.Existing[0].State != impact.AcceptRouted || p.Existing[0].By != "bob@acme.com" {
		t.Fatalf("existing = %+v", p.Existing)
	}
}

func TestProposedAcceptsRangesOnly(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Proposed = true
	for _, f := range planFields(e.plan(t, opts)) {
		if strings.Contains(f, ".ref") {
			t.Fatalf("--proposed accepted %s", f)
		}
	}
	opts.Fields = []string{"dependencies[ledger].ref"}
	p := e.plan(t, opts)
	if len(p.Ranges) != 3 || p.Ranges[2].Field != "dependencies[ledger].ref" || p.Ranges[2].Value != "oci://"+e.host+"/acme/ledger" || p.Ranges[2].Source != indexSrc {
		t.Fatalf("ranges = %+v", p.Ranges)
	}
}

func TestAcceptPlanNothingProposed(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.publishBundle(t, e.repo, "1.7.0", e.reportsFiles("1.7.0", false), nil)
	opts := e.trust()
	opts.Proposed = true
	if de := e.refusedAccept(t, opts); de.Code != codeAcceptNothingProposed {
		t.Fatalf("refusal = %+v", de)
	}
}

func TestAcceptFieldWithValue(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Fields = []string{"dependencies[ledger].compatibility=^2.4.0"}
	p := e.plan(t, opts)
	if len(p.Ranges) != 1 || p.Ranges[0].Value != "^2.4.0" || p.Ranges[0].Proposed != "^2.3.0" {
		t.Fatalf("ranges = %+v", p.Ranges)
	}
	opts.Fields = []string{"dependencies[ledger].compatibility"}
	if p := e.plan(t, opts); p.Ranges[0].Value != "^2.3.0" {
		t.Fatalf("ranges = %+v", p.Ranges)
	}
	for _, f := range []string{"dependencies[ledger].compatibility=unknown", "dependencies[ledger].compatibility=not a range", "dependencies[ledger].ref=oci://x/y"} {
		opts.Fields = []string{f}
		if de := e.refusedAccept(t, opts); de.Code != codeFieldNotAcceptable {
			t.Errorf("%s: %+v", f, de)
		}
	}
}

func TestAcceptFieldNotAcceptable(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Fields = []string{"ports[8080]"}
	if de := e.refusedAccept(t, opts); de.Code != codeFieldNotAcceptable || !strings.Contains(de.CLI, "pacto declare") {
		t.Fatalf("refusal = %+v", de)
	}
}

func TestAcceptPlanBreakFromUnpublishedProvider(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Break, opts.From = "ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "2026-12-31", "billing", "reports"))
	p := e.plan(t, opts)
	entry := contract.Break{Consumers: []string{"reports", "billing"}, Reason: "the v1 API is removed", Expires: "2026-12-31"}
	want := delivery.AcceptedBreak{Provider: "ledger", Version: "3.0.0", Announcement: delivery.BreakAnnouncement(entry)}
	if p.Kind != delivery.AcceptKindBreak || p.Break == nil || *p.Break != want || p.Expires != "2026-12-31" || len(p.Ranges) != 0 {
		t.Fatalf("plan = %+v %+v", p, p.Break)
	}
}

func TestAcceptPlanBreakFromPublishedProvider(t *testing.T) {
	e := newAcceptEnv(t, false)
	files := ledgerFiles("3.0.0", "", "reports")
	files[contract.InferredRecordFile] = string(acceptRecordOf(
		contract.Inferred{Field: "breaks[0].expires", Value: "2026-12-14", Source: "commit:0123456789ab +30d", Confidence: contract.ConfidenceLikely}).Marshal())
	d := e.publishBundle(t, e.host+"/acme/ledger", "3.0.0", files, nil)
	opts := e.trust()
	opts.Break = "ledger@3.0.0"
	p := e.plan(t, opts)
	if p.Break.Bundle != e.host+"/acme/ledger@"+d || p.Expires != "2026-12-14" {
		t.Fatalf("break = %+v, expires %q", p.Break, p.Expires)
	}
	// A published version without a record proposes no expiry.
	e.publishBundle(t, e.host+"/acme/ledger", "3.1.0", ledgerFiles("3.1.0", "", "reports"), nil)
	opts.Break = "ledger@3.1.0"
	if p := e.plan(t, opts); p.Expires != "" {
		t.Fatalf("expires %q", p.Expires)
	}
}

func TestAcceptBreakNotAnnounced(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	for name, o := range map[string]struct{ brk, from string }{
		"other consumer":  {"ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "", "billing"))},
		"no from":         {"ledger@3.0.0", ""},
		"other provider":  {"ledger@3.0.0", writeDir(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: audit, version: 3.0.0}\n"})},
		"unknown by name": {"vault@1.0.0", ""},
	} {
		opts.Break, opts.From = o.brk, o.from
		if de := e.refusedAccept(t, opts); de.Code != codeBreakNotAnnounced || !strings.Contains(de.CLI, "--from") {
			t.Errorf("%s: %+v", name, de)
		}
	}
	for _, bad := range []string{"ledger", "@3.0.0", "ledger@"} {
		opts.Break, opts.From = bad, ""
		if _, err := e.svc.AcceptPlan(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "<provider>@<version>") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestAcceptPlanBreakFromPushedRef(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.publishBundle(t, e.host+"/acme/ledger-rc", "3.0.0-rc.1", ledgerFiles("3.0.0", "", "reports"), nil)
	opts := e.trust()
	opts.Break, opts.From = "ledger@3.0.0", "oci://"+e.host+"/acme/ledger-rc:3.0.0-rc.1"
	if p := e.plan(t, opts); p.Break.Bundle != "" || p.Expires != "" {
		t.Fatalf("break = %+v", p.Break)
	}
	opts.From = filepath.Join(t.TempDir(), "missing")
	if _, err := e.svc.AcceptPlan(t.Context(), opts); err == nil {
		t.Fatal("a missing checkout planned")
	}
}

func TestAcceptNotPublished(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Ref, opts.Proposed = "oci://"+e.host+"/acme/nothing", true
	if de := e.refusedAccept(t, opts); de.Code != codeNotPublished {
		t.Fatalf("refusal = %+v", de)
	}
	opts.Ref, opts.Dir = "", writeDir(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: reports, version: 1.0.0}\n"})
	if de := e.refusedAccept(t, opts); de.Code != codeNotPublished || !strings.Contains(de.CLI, opts.Dir) {
		t.Fatalf("refusal = %+v", de)
	}
	// A checkout names the repository its publish section publishes to.
	opts.Dir = writeDir(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: reports, version: 1.0.0}\npublish:\n  bundle: oci://" + e.repo + "\n"})
	if p := e.plan(t, opts); p.Subject != e.subject {
		t.Fatalf("subject = %s", p.Subject)
	}
	opts.Dir = filepath.Join(t.TempDir(), "missing")
	if _, err := e.svc.AcceptPlan(t.Context(), opts); err == nil {
		t.Fatal("a missing checkout planned")
	}
}

func TestAcceptPlanOnlyCandidatesIsNotPublished(t *testing.T) {
	e := newAcceptEnv(t, false)
	repo := e.host + "/acme/drafty"
	e.publishBundle(t, repo, "2.0.0-rc.1", e.reportsFiles("2.0.0-rc.1", true), nil)
	opts := e.trust()
	opts.Ref, opts.Proposed = repo, true
	if de := e.refusedAccept(t, opts); de.Code != codeNotPublished {
		t.Fatalf("refusal = %+v", de)
	}
}

func TestAcceptPlanInferredOwnerMayNotAccept(t *testing.T) {
	e := newAcceptEnv(t, false)
	files := e.reportsFiles("1.7.0", false)
	files[contract.InferredRecordFile] = string(acceptRecordOf(
		contract.Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0", Source: snapSrc, Confidence: contract.ConfidenceLikely},
		contract.Inferred{Field: "service.owner", Value: "reporting", Source: "CODEOWNERS:1", Confidence: contract.ConfidenceLikely, Class: contract.InferredClassOwnerFile},
	).Marshal())
	files["CODEOWNERS"] = "* @reporting\n"
	e.publishBundle(t, e.repo, "1.7.0", files, nil)
	opts := e.trust()
	opts.Proposed = true
	if p := e.plan(t, opts); !strings.Contains(p.WhoMayAccept, "reports declares no owner") {
		t.Fatalf("who = %q", p.WhoMayAccept)
	}
}

func TestAcceptPlanRegistryFailures(t *testing.T) {
	e := newAcceptEnv(t, false)
	ledger := e.host + "/acme/ledger"
	e.publishBundle(t, ledger, "3.0.0", ledgerFiles("3.0.0", "", "reports"), nil)
	opts := e.trust()
	opts.Proposed = true
	for name, c := range map[string]struct {
		fail func(*http.Request) bool
		brk  string
	}{
		"tags": {fail: func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/reports/tags/list") }},
		"head": {fail: func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/manifests/1.6.0") }},
		"pull": {fail: func(r *http.Request) bool {
			return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/reports/manifests/sha256:")
		}},
		"acceptances":   {fail: func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }},
		"provider head": {fail: func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/ledger/manifests/3.0.0") }, brk: "ledger@3.0.0"},
	} {
		e.failWhen(c.fail)
		opts.Break = c.brk
		if _, err := e.svc.AcceptPlan(t.Context(), opts); err == nil {
			t.Errorf("%s: a failed read planned", name)
		}
	}
	e.failWhen(nil)
}

func TestAcceptPlanUnreadableRecord(t *testing.T) {
	e := newAcceptEnv(t, false)
	files := e.reportsFiles("1.7.0", false)
	files[contract.InferredRecordFile] = "format: [\n"
	e.publishBundle(t, e.repo, "1.7.0", files, nil)
	opts := e.trust()
	opts.Proposed = true
	if _, err := e.svc.AcceptPlan(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "the inferred record") {
		t.Fatalf("err = %v", err)
	}
}

func TestAcceptPlanBreakOfDependencyByRef(t *testing.T) {
	e := newAcceptEnv(t, false)
	files := ledgerFiles("2.0.0", "2026-12-31", "reports")
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "name: ledger", "name: billing", 1)
	d := e.publishBundle(t, e.host+"/acme/billing", "2.0.0", files, nil)
	opts := e.trust()
	opts.Break = "billing@2.0.0"
	if p := e.plan(t, opts); p.Break.Bundle != e.host+"/acme/billing@"+d {
		t.Fatalf("break = %+v", p.Break)
	}
}

func TestRepoOf(t *testing.T) {
	for ref, want := range map[string]string{"oci://h:5000/a/b": "h:5000/a/b", "oci://h/a/b:1.0.0": "h/a/b", "h/a/b@sha256:00": "h/a/b"} {
		if got := repoOf(ref); got != want {
			t.Errorf("repoOf(%q) = %q", ref, got)
		}
	}
	if recordedExpiry(acceptRecordOf(contract.Inferred{Field: "breaks[1].expires"}), 0) != "" {
		t.Error("another break's expiry")
	}
}

func TestAcceptPlanListsBreakAcceptances(t *testing.T) {
	e := newAcceptEnv(t, false)
	entry := contract.Break{Consumers: []string{"reports"}, Reason: "the v1 API is removed", Expires: "2026-12-31"}
	brk := &delivery.AcceptedBreak{Provider: "ledger", Version: "3.0.0", Announcement: delivery.BreakAnnouncement(entry)}
	record := func(b *delivery.AcceptedBreak) delivery.AcceptanceRecord {
		return delivery.AcceptanceRecord{Service: "reports", Version: "1.6.0", Kind: delivery.AcceptKindBreak, Break: b}
	}
	other := *brk
	other.Announcement = "sha256:00"
	e.attach(t, attachment{record: rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	e.attach(t, attachment{record: record(&other), token: personTok("ana@acme.com"), tag: true})
	e.attach(t, attachment{record: record(brk), token: personTok("ana@acme.com"), tag: true})
	opts := e.trust()
	opts.Break, opts.From = "ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "2026-12-31", "reports"))
	p := e.plan(t, opts)
	last := p.Existing[len(p.Existing)-1]
	if last.State != impact.AcceptCounted || last.Break == nil || last.Break.Announcement != brk.Announcement {
		t.Fatalf("existing = %+v", p.Existing)
	}
	for _, a := range p.Existing {
		if a.Break != nil && a.Break.Announcement == "sha256:00" && a.State != impact.AcceptExpired {
			t.Errorf("another break's acceptance judged %s", a.State)
		}
	}
	// A ranges plan reads past break acceptances to its own kind.
	opts.Break, opts.From, opts.Proposed = "", "", true
	if p := e.plan(t, opts); len(p.Existing) == 0 || p.Existing[len(p.Existing)-1].Break != nil {
		t.Fatalf("existing = %+v", p.Existing)
	}
}
