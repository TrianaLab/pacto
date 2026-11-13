package delivery

import "testing"

// SPEC 21 rule 1: whether the promoter trusts an upstream record stream is
// read from the Stage's Ready condition.
func TestRecordsTrustReadsTheReadyCondition(t *testing.T) {
	cases := map[string][]Condition{
		"unknown":    nil,
		"trusted":    {{Type: "RecordStoreReady", Status: "False"}, {Type: "Ready", Status: "True", Reason: "Ready"}},
		"untrusted":  {{Type: "Ready", Status: "False", Reason: "UpstreamUntrusted"}},
		"unreadable": {{Type: "Ready", Status: "False", Reason: "UpstreamUnreadable"}},
	}
	for want, conds := range cases {
		st := stage("prod", "pay", "pay", StageStatus{Conditions: conds})
		if got := recordsTrust(&st); got != want {
			t.Errorf("%v: got %s, want %s", conds, got, want)
		}
	}
	st := stage("prod", "pay", "pay", StageStatus{Conditions: []Condition{{Type: "Ready", Status: "False", Reason: "ApplierNotFound"}}})
	if got := recordsTrust(&st); got != "unknown" {
		t.Errorf("a Stage not Ready for its applier never judged the records: got %s", got)
	}
}

// TestUpstreamListed: an upstream Stage is read from the listed Stages; one
// not listed is missing only when its namespace was read in full, out of
// scope outside the reader's namespace, else unreadable. The other kinds need
// no read.
func TestUpstreamListed(t *testing.T) {
	up := stage("test", "pay", "other", StageStatus{})
	up.Spec.Environment = "staging"
	byStage := map[string]*StageObject{"test/pay": &up}
	none := func(string) bool { return false }
	at := func(c Candidates, read func(string) bool, scope string, ready ...string) Upstream {
		st := stage("prod", "pay", "pay", StageStatus{})
		st.Spec.Candidates = c
		for _, r := range ready {
			st.Status.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: r}}
		}
		return *UpstreamListed(&st, byStage, read, scope)
	}
	upTest, gone := &StageRef{Namespace: "test", Name: "pay"}, &StageRef{Namespace: "test", Name: "gone"}
	cases := []struct {
		got, want Upstream
	}{
		{at(Candidates{UpstreamStage: upTest}, none, ""), Upstream{Kind: "stage", Namespace: "test", Stage: "pay", Environment: "staging"}},
		{at(Candidates{UpstreamStage: gone}, func(ns string) bool { return ns == "test" }, ""), Upstream{Kind: "stage", Namespace: "test", Stage: "gone", Problem: "missing"}},
		{at(Candidates{UpstreamStage: gone}, none, "prod"), Upstream{Kind: "stage", Namespace: "test", Stage: "gone", Problem: "outOfScope"}},
		{at(Candidates{UpstreamStage: gone}, none, ""), Upstream{Kind: "stage", Namespace: "test", Stage: "gone", Problem: "unreadable"}},
		{at(Candidates{UpstreamStage: &StageRef{Name: "pay"}}, none, ""), Upstream{Kind: "stage", Namespace: "prod", Stage: "pay", Problem: "self"}},
		{at(Candidates{UpstreamRecords: &RemoteStage{Repository: "r/s", Stream: "c/n/s"}}, nil, ""), Upstream{Kind: "records", Stream: "c/n/s", Repository: "r/s", Trust: "unknown"}},
		{at(Candidates{OCIRepositoryRef: &LocalRef{Name: "pc"}}, nil, ""), Upstream{Kind: "artifact", Repository: "pc"}},
		{at(Candidates{OCIRepositoryRef: &LocalRef{Name: "pc"}}, nil, "", ReadyReasonCandidateNotFound), Upstream{Kind: "artifact", Repository: "pc", Problem: "missing"}},
		{at(Candidates{OCIRepositoryRef: &LocalRef{Name: "pc"}}, nil, "", ReadyReasonCandidateUnverified), Upstream{Kind: "artifact", Repository: "pc", Problem: "unverified"}},
		{at(Candidates{}, nil, ""), Upstream{Kind: "none"}},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %+v, want %+v", i, c.got, c.want)
		}
	}
}
