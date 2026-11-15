package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/readiness"
)

// validated marks revision version of q's "svc" as validated with fs.
func validated(q *Query, version string, fs ...finding.Finding) *ContractRevision {
	r := q.snap.Revisions[RevisionKey("svc@"+version)]
	r.Stages = []ValidationStage{{Name: "structural", Status: "pass"}}
	r.Validation = fs
	return r
}

func fnd(code finding.Code, sev finding.Severity, kind, name string) finding.Finding {
	return finding.Finding{Code: code, Severity: sev, Subject: finding.SubjectRef{Kind: kind, Name: name}}
}

func twoRevisions() *Query {
	return changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{owned("a"), owned("a")})
}

func TestChangesFindingAppeared(t *testing.T) {
	q := twoRevisions()
	validated(q, "v1.0.0", fnd("A", finding.SeverityWarning, "", ""))
	// The same code on two subjects is two findings; a repeat on one subject is one.
	validated(q, "v2.0.0", fnd("A", finding.SeverityWarning, "", ""),
		fnd("B", finding.SeverityError, "interface", "api"), fnd("B", finding.SeverityError, "interface", "api"),
		fnd("B", finding.SeverityError, "interface", "admin"))
	h := svcChanges(q)
	checkChanges(t, only(h, "finding"), []string{
		"v2.0.0 finding B (interface admin) added  ->error v1.0.0",
		"v2.0.0 finding B (interface api) added  ->error v1.0.0",
	})
	for _, e := range h.Items {
		if e.Kind == "finding" && (e.Source != "validation" || e.Provenance != "declared") {
			t.Errorf("event %+v", e)
		}
	}
	if l := h.Lines(); l[0] != "  svc v2.0.0  finding B (interface admin) added: error, compared with svc v1.0.0  time not recorded" {
		t.Errorf("lines = %q", l)
	}
}

func TestChangesFindingResolved(t *testing.T) {
	q := twoRevisions()
	validated(q, "v1.0.0", fnd("A", finding.SeverityWarning, "dependency", "db"))
	validated(q, "v2.0.0")
	checkChanges(t, only(svcChanges(q), "finding"), []string{"v2.0.0 finding A (dependency db) removed  warning-> v1.0.0"})
}

func TestChangesReadinessCrossed(t *testing.T) {
	q := twoRevisions()
	validated(q, "v1.0.0").Readiness = &readiness.Result{Score: 60, MinScore: 80}
	validated(q, "v2.0.0").Readiness = &readiness.Result{Score: 90, MinScore: 80}
	checkChanges(t, only(svcChanges(q), "finding"), []string{"v2.0.0 finding readiness changed  score 60/80->score 90/80 v1.0.0"})
	// A score that moves without crossing the minimum, or a side with no readiness, is no event.
	q.snap.Revisions["svc@v1.0.0"].Readiness.Score = 85
	checkChanges(t, only(svcChanges(q), "finding"), nil)
	q.snap.Revisions["svc@v1.0.0"].Readiness = nil
	checkChanges(t, only(svcChanges(q), "finding"), nil)
}

func TestChangesFindingNotForOldest(t *testing.T) {
	q := changesQuery([]string{"v1.0.0"}, []*contract.Contract{owned("a")})
	validated(q, "v1.0.0", fnd("A", finding.SeverityError, "", "")).Readiness = &readiness.Result{Score: 10, MinScore: 80}
	checkChanges(t, only(svcChanges(q), "finding"), nil)
}

func TestChangesFindingSkipsUnvalidated(t *testing.T) {
	q := twoRevisions()
	validated(q, "v2.0.0", fnd("A", finding.SeverityError, "", ""))
	checkChanges(t, only(svcChanges(q), "finding"), nil)
	q = twoRevisions()
	validated(q, "v1.0.0", fnd("A", finding.SeverityError, "", ""))
	checkChanges(t, only(svcChanges(q), "finding"), nil)
}
