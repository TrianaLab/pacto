package stats

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

var opSeq int

// op builds an operation record of orders on the prod Stage finished at.
func op(typ string, outcome delivery.Outcome, verification delivery.Verification, at time.Time) runrecord.Record {
	opSeq++
	return runrecord.Record{
		Version: runrecord.Version, ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", opSeq),
		Kind: runrecord.KindOperation, Service: "orders", Outcome: string(outcome), Type: typ,
		Verification: string(verification), RecordedAt: at, Environment: "production",
		Stage:      &runrecord.StageRef{Namespace: "shop", Name: "prod", Cluster: "c1"},
		FinishedAt: at.Format(time.RFC3339),
	}
}

func promote(outcome delivery.Outcome, verification delivery.Verification, at time.Time) runrecord.Record {
	return op(opPromote, outcome, verification, at)
}

func verifiedTo(to string, at time.Time) runrecord.Record {
	r := promote(delivery.Succeeded, delivery.Verified, at)
	r.To = to
	return r
}

func published(service, deploy, bundle string, at time.Time) runrecord.Record {
	r := rec(runrecord.KindPublish, "sha256:"+service+deploy+bundle, at)
	r.Service, r.DeployDigest, r.BundleDigest = service, deploy, bundle
	r.ID = runrecord.ID(runrecord.KindPublish, service, r.OldRevision, r.NewRevision)
	return r
}

func envs(recs ...runrecord.Record) []EnvironmentMetrics {
	return agg(set(recs...)).Totals.Environments
}

func onlyEnv(t *testing.T, recs ...runrecord.Record) EnvironmentMetrics {
	t.Helper()
	es := envs(recs...)
	if len(es) != 1 {
		t.Fatalf("environments = %+v", es)
	}
	return es[0]
}

func digest(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func TestEnvironmentRatesPerEnvironment(t *testing.T) {
	staging := promote(delivery.Succeeded, delivery.Verified, hoursAgo(2))
	staging.Environment = "staging"
	es := envs(promote(delivery.Failed, "", hoursAgo(1)), staging, promote(delivery.Succeeded, delivery.Verified, hoursAgo(3)))
	if len(es) != 2 || es[0].Environment != "production" || es[1].Environment != "staging" {
		t.Fatalf("environments = %+v", es)
	}
	if p := es[0]; p.Operations != 2 || p.Failed != (Ratio{Count: 1, Of: 2}) || p.Source != "context" {
		t.Fatalf("production = %+v", p)
	}
	if s := es[1]; s.Operations != 1 || s.Failed != (Ratio{Count: 0, Of: 1}) {
		t.Fatalf("staging = %+v", s)
	}
}

func TestEnvironmentWithoutPromotesIsUnknown(t *testing.T) {
	e := onlyEnv(t, op(opRollback, delivery.Succeeded, delivery.Verified, hoursAgo(1)))
	unknown := Ratio{Unknown: true}
	if e.Held != unknown || e.Failed != unknown || e.RolledBack != unknown || e.VerificationFailed != unknown || !e.LeadTime.Unknown {
		t.Fatalf("environment = %+v", e)
	}
}

func TestHeldUsesHeldFor(t *testing.T) {
	held := promote(delivery.Succeeded, delivery.Verified, hoursAgo(1))
	held.HeldFor = "2h0m0s"
	zero := promote(delivery.Succeeded, delivery.Verified, hoursAgo(2))
	zero.HeldFor = "0s"
	if e := onlyEnv(t, held, zero, promote(delivery.Succeeded, delivery.Verified, hoursAgo(3))); e.Held != (Ratio{Count: 1, Of: 3}) {
		t.Fatalf("held = %+v", e.Held)
	}
}

func TestHeldUnparsableHeldForIsNotHeld(t *testing.T) {
	r := promote(delivery.Succeeded, delivery.Verified, hoursAgo(1))
	r.HeldFor = "a while"
	if e := onlyEnv(t, r); e.Held != (Ratio{Count: 0, Of: 1}) || e.Operations != 1 {
		t.Fatalf("environment = %+v", e)
	}
}

func TestFailedCountsErrored(t *testing.T) {
	e := onlyEnv(t, promote(delivery.Failed, "", hoursAgo(1)), promote(delivery.Errored, "", hoursAgo(2)),
		promote(delivery.Cancelled, "", hoursAgo(3)), promote(delivery.Succeeded, delivery.Verified, hoursAgo(4)))
	if e.Failed != (Ratio{Count: 2, Of: 4}) {
		t.Fatalf("failed = %+v", e.Failed)
	}
}

func TestRollbackRate(t *testing.T) {
	var recs []runrecord.Record
	for i := range 41 {
		recs = append(recs, promote(delivery.Succeeded, delivery.Verified, hoursAgo(i+1)))
	}
	recs = append(recs, promote(delivery.Failed, "", hoursAgo(50)))
	for i := range 3 {
		recs = append(recs, op(opRollback, delivery.Succeeded, delivery.Verified, hoursAgo(i+1)))
	}
	recs = append(recs, op(opRollback, delivery.Failed, "", hoursAgo(5)))
	if e := onlyEnv(t, recs...); e.RolledBack != (Ratio{Count: 3, Of: 41}) || e.Operations != 46 {
		t.Fatalf("environment = %+v", e)
	}
}

func TestVerificationFailedRate(t *testing.T) {
	e := onlyEnv(t, promote(delivery.Succeeded, delivery.Verified, hoursAgo(1)), promote(delivery.Succeeded, delivery.Unverified, hoursAgo(2)),
		promote(delivery.Succeeded, delivery.AcceptedUnknown, hoursAgo(3)), promote(delivery.Failed, "", hoursAgo(4)))
	if e.VerificationFailed != (Ratio{Count: 1, Of: 2}) || e.AcceptedUnknown != 1 {
		t.Fatalf("environment = %+v", e)
	}
}

func TestUnnamedOperationTypeCountsInNoRate(t *testing.T) {
	ack := op("acknowledge", delivery.Failed, delivery.Unverified, hoursAgo(1))
	ack.HeldFor = "1h0m0s"
	e := onlyEnv(t, ack, promote(delivery.Succeeded, delivery.Verified, hoursAgo(2)))
	want := Ratio{Count: 0, Of: 1}
	if e.Operations != 2 || e.Held != want || e.Failed != want || e.VerificationFailed != want || e.AcceptedUnknown != 0 {
		t.Fatalf("environment = %+v", e)
	}
}

func TestEnvironmentReadsTopLevelEnvironment(t *testing.T) {
	if e := onlyEnv(t, promote(delivery.Succeeded, delivery.Verified, hoursAgo(1))); e.Environment != "production" || e.Source != "context" {
		t.Fatalf("environment = %+v", e)
	}
}

func TestEnvironmentFallsBackToStageName(t *testing.T) {
	r := promote(delivery.Succeeded, delivery.Verified, hoursAgo(1))
	r.Environment = ""
	if e := onlyEnv(t, r); e.Environment != "prod" || e.Source != "stage" {
		t.Fatalf("environment = %+v", e)
	}
}

func TestEnvironmentOfCIRecord(t *testing.T) {
	r := rec(runrecord.KindDiff, "sha256:new", hoursAgo(1))
	if _, _, ok := environmentOf(r); ok {
		t.Fatal("a CI record without an environment joined one")
	}
	r.Environment = "ci"
	if env, src, ok := environmentOf(r); env != "ci" || src != "context" || !ok {
		t.Fatalf("environmentOf = %q %q %v", env, src, ok)
	}
	if es := envs(r); es != nil {
		t.Fatalf("a CI record made an environment row: %+v", es)
	}
}

func leadOf(t *testing.T, recs ...runrecord.Record) LeadTime {
	t.Helper()
	return onlyEnv(t, recs...).LeadTime
}

func secs(n int64) *int64 { return &n }

func assertLead(t *testing.T, got LeadTime, p50, p90 int64, joined, unjoined int) {
	t.Helper()
	if got.P50Seconds == nil || got.P90Seconds == nil || *got.P50Seconds != p50 || *got.P90Seconds != p90 || got.Joined != joined || got.Unjoined != unjoined || got.Unknown {
		t.Fatalf("lead time = %+v (p50 %v p90 %v)", got, got.P50Seconds, got.P90Seconds)
	}
}

func TestLeadTimeJoinsDeployDigest(t *testing.T) {
	assertLead(t, leadOf(t, published("orders", digest('d'), digest('b'), hoursAgo(3)), verifiedTo(digest('d'), hoursAgo(1))), 7200, 7200, 1, 0)
}

func TestLeadTimeFallsBackToBundleDigest(t *testing.T) {
	p := verifiedTo(digest('x'), hoursAgo(1))
	p.Contract = digest('b')
	assertLead(t, leadOf(t, published("orders", digest('d'), digest('b'), hoursAgo(2)), p), 3600, 3600, 1, 0)
}

func TestLeadTimeJoinsPublishBeforeWindow(t *testing.T) {
	assertLead(t, leadOf(t, published("orders", digest('d'), "", aggWindow.Since.Add(-time.Hour)), verifiedTo(digest('d'), aggWindow.Since.Add(time.Hour))), 7200, 7200, 1, 0)
}

func TestLeadTimeUnjoinedIsUnknown(t *testing.T) {
	st := agg(set(verifiedTo(digest('d'), hoursAgo(1)), verifiedTo("", hoursAgo(2))))
	lt := st.Totals.Environments[0].LeadTime
	if lt != (LeadTime{Unjoined: 2, Unknown: true}) {
		t.Fatalf("lead time = %+v", lt)
	}
	var found bool
	for _, l := range st.Limitations {
		if l.Code == LimitationLeadTimeUnjoined {
			found = l.Message == "2 promotions have no publish record with their digest, so their lead time is unknown"
		}
	}
	if !found || st.Completeness != "complete" {
		t.Fatalf("limitations %+v completeness %s", st.Limitations, st.Completeness)
	}
}

func TestLeadTimeUnparsableFinishedAtIsUnjoined(t *testing.T) {
	p := verifiedTo(digest('d'), hoursAgo(1))
	p.FinishedAt = "yesterday"
	lt := leadTimes([]Entry{{Record: p}}, []Entry{{Record: published("orders", digest('d'), "", hoursAgo(2))}})
	if lt["production"] != (LeadTime{Unjoined: 1, Unknown: true}) {
		t.Fatalf("lead time = %+v", lt)
	}
}

func TestLeadTimeFirstPromotePerStage(t *testing.T) {
	assertLead(t, leadOf(t, published("orders", digest('d'), "", hoursAgo(10)),
		verifiedTo(digest('d'), hoursAgo(2)), verifiedTo(digest('d'), hoursAgo(8)), verifiedTo(digest('d'), hoursAgo(1)),
		promote(delivery.Succeeded, delivery.Unverified, hoursAgo(9))), 7200, 7200, 1, 0)
}

func TestLeadTimeNegativeIsUnjoined(t *testing.T) {
	if lt := leadOf(t, published("orders", digest('d'), "", hoursAgo(1)), verifiedTo(digest('d'), hoursAgo(2))); lt != (LeadTime{Unjoined: 1, Unknown: true}) {
		t.Fatalf("lead time = %+v", lt)
	}
}

func TestLeadTimeNeverJoinsOtherServicePublish(t *testing.T) {
	if lt := leadOf(t, published("payments", digest('d'), digest('d'), hoursAgo(3)), verifiedTo(digest('d'), hoursAgo(1))); lt != (LeadTime{Unjoined: 1, Unknown: true}) {
		t.Fatalf("lead time = %+v", lt)
	}
}

func TestLeadTimeUsesNewestPublishOfDigest(t *testing.T) {
	assertLead(t, leadOf(t, published("orders", digest('d'), "", hoursAgo(5)), published("orders", digest('d'), "", hoursAgo(3)),
		verifiedTo(digest('d'), hoursAgo(1))), 7200, 7200, 1, 0)
}

func TestLeadTimePercentiles(t *testing.T) {
	for _, tc := range []struct {
		n        int
		p50, p90 int64
	}{{1, 60, 60}, {2, 60, 120}, {10, 300, 540}} {
		var recs []runrecord.Record
		for i := range tc.n {
			to := digest(byte('a' + i))
			recs = append(recs, published("orders", to, "", hoursAgo(1).Add(-time.Duration(i+1)*time.Minute)), verifiedTo(to, hoursAgo(1)))
		}
		assertLead(t, leadOf(t, recs...), tc.p50, tc.p90, tc.n, 0)
	}
	if percentile(nil, 50) != nil || *percentile([]int64{1, 2, 3}, 50) != *secs(2) {
		t.Fatal("percentile")
	}
}
