package fleet

import (
	"strings"
	"testing"
	"time"
)

func TestTargetReportedLine(t *testing.T) {
	now := fixedNow()
	at := now.Add(-3 * time.Minute)
	reported := &TargetRecord{Scope: "my-idp", ReportedBy: "my-idp", EvidenceAt: &at}
	if got := reported.ReportedLine(now); got != "Reported by: my-idp (evidence 3m ago)" {
		t.Errorf("line = %q", got)
	}
	if got := (&TargetRecord{ReportedBy: "my-idp"}).ReportedLine(now); got != "Reported by: my-idp (evidence age unknown)" {
		t.Errorf("undated line = %q", got)
	}
	// A target-state file target of kind external keeps its Scope and says
	// nobody reported it.
	if got := (&TargetRecord{Scope: "acme-idp", Kind: "external", EvidenceAt: &at}).ReportedLine(now); got != "" {
		t.Errorf("a target no Evidence Server delivered reads %q", got)
	}
	detail := &TargetDetailData{Scope: "my-idp", ReportedBy: "my-idp", EvidenceAt: &at}
	if got := detail.ReportedPhrase(now); got != "my-idp (evidence 3m ago)" {
		t.Errorf("phrase = %q", got)
	}
	if got := (&TargetDetailData{Scope: "acme-idp"}).ReportedPhrase(now); got != "" {
		t.Errorf("unreported phrase = %q", got)
	}
}

func TestServiceContributionLine(t *testing.T) {
	now := fixedNow()
	digest := "sha256:ab12" + strings.Repeat("0", 60)
	fresh := &ServiceContribution{Producer: "my-idp", Repository: ctbRepo, Ref: ctbRepo + "@" + digest,
		AsOf: ptrTime(now.Add(-3 * time.Hour)), ReadAt: ptrTime(now.Add(-2 * time.Minute))}
	want := "Contributed by my-idp from " + ctbRepo + "@sha256:ab1200000000…, recorded 3h ago, read 2m ago; inferred, never clears a gate"
	if got := fresh.Line(now); got != want {
		t.Errorf("fresh = %q\nwant  %q", got, want)
	}
	stale := &ServiceContribution{Producer: "my-idp", Ref: "plain-ref", AsOf: ptrTime(now.Add(-40 * 24 * time.Hour)), Stale: true}
	if got := stale.Line(now); got != "Contributed by my-idp from plain-ref, recorded 40d ago, stale; inferred, never clears a gate" {
		t.Errorf("stale = %q", got)
	}
	undated := &ServiceContribution{Producer: "my-idp", Ref: "plain-ref", Stale: true}
	if got := undated.Line(now); got != "Contributed by my-idp from plain-ref, recorded at an unknown time, stale; inferred, never clears a gate" {
		t.Errorf("undated = %q", got)
	}
}
