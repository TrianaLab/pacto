package delivery

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestWindowEntries(t *testing.T) {
	at := func(h int) string { return time.Date(2026, 11, 5, h, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	tm := func(h int) time.Time { return time.Date(2026, 11, 5, h, 0, 0, 0, time.UTC) }
	entries := []HistoryEntry{
		{Operation: "applied-finished", Applied: at(10), Finished: at(11)},
		{Operation: "finished-only", Finished: at(12)},
		{Operation: "in-flight", Since: at(9)},
		{Operation: "timeless"},
		{Operation: "unparseable", Finished: "yesterday"},
		{Operation: "unparseable-finish", Applied: at(1), Finished: "later"},
		{Operation: "applied-before-finished-inside", Applied: at(7), Finished: at(10)},
		{Operation: "finished-before", Applied: at(5), Finished: at(6)},
		{Operation: "started-after", Since: at(20)},
	}
	h := History{Completeness: "partial", Note: "n", Problems: []string{"p"}, Unreadable: []string{"u"}, Entries: entries}
	all := []string{"applied-finished", "finished-only", "in-flight", "timeless", "unparseable", "unparseable-finish",
		"applied-before-finished-inside", "finished-before", "started-after"}
	for _, c := range []struct {
		name         string
		since, until time.Time
		want         []string
		note         string
	}{
		{"identity", time.Time{}, time.Time{}, all, "n"},
		{"window", tm(8), tm(12), []string{"applied-finished", "finished-only", "in-flight", "timeless", "unparseable",
			"unparseable-finish", "applied-before-finished-inside"}, "n; only changes between 2026-11-05T08:00:00Z and 2026-11-05T12:00:00Z are listed"},
		{"open start", time.Time{}, tm(8), []string{"timeless", "unparseable", "unparseable-finish", "applied-before-finished-inside",
			"finished-before"}, "n; only changes at or before 2026-11-05T08:00:00Z are listed"},
		{"open end", tm(11), time.Time{}, []string{"applied-finished", "finished-only", "in-flight", "timeless", "unparseable",
			"unparseable-finish", "started-after"}, "n; only changes at or after 2026-11-05T11:00:00Z are listed"},
		{"nothing dropped", tm(0), tm(23), all, "n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := WindowEntries(h, c.since, c.until)
			var names []string
			for _, e := range got.Entries {
				names = append(names, e.Operation)
			}
			if !slices.Equal(names, c.want) || got.Note != c.note {
				t.Fatalf("entries = %v note %q", names, got.Note)
			}
			if got.Completeness != "partial" || got.Problems[0] != "p" || got.Unreadable[0] != "u" {
				t.Fatalf("window changed the rest of the history: %+v", got)
			}
		})
	}
	if len(h.Entries) != len(entries) || h.Entries[7].Operation != "finished-before" || h.Note != "n" {
		t.Fatalf("the input history was mutated: %+v", h)
	}
}

func TestOfService(t *testing.T) {
	st := func(name, domain string) StageObject {
		return StageObject{Spec: StageSpec{Service: ServiceRef{Name: name, Domain: domain}}}
	}
	for _, c := range []struct {
		st              StageObject
		service, domain string
		want            bool
	}{
		{st("orders", "a"), "", "", true},
		{st("orders", ""), "orders", "a", true},
		{st("orders", "a"), "orders", "a", true},
		{st("orders", "b"), "orders", "a", false},
		{st("payments", ""), "orders", "", false},
	} {
		if got := OfService(c.st, c.service, c.domain); got != c.want {
			t.Errorf("OfService(%+v, %q, %q) = %v", c.st.Spec.Service, c.service, c.domain, got)
		}
	}
}

func TestStatusEntryCopiesAppliedCauseCorrelation(t *testing.T) {
	o := ansOp("a", "u1", 1, "", ansA)
	o.Status.Times.Applied, o.Spec.CorrelationID = "2026-11-05T10:00:00Z", "incident-7"
	o.Spec.Cause = &Cause{Operation: "u0"}
	e := StatusEntry(o)
	if e.Applied != "2026-11-05T10:00:00Z" || e.CorrelationID != "incident-7" || e.Cause == nil || e.Cause.Operation != "u0" {
		t.Fatalf("status entry = %+v", e)
	}
}

func TestRecordEntryAppliedFromVerifyInput(t *testing.T) {
	const input = `{"window":{"from":"2026-11-05T10:00:00Z","to":"2026-11-05T10:15:00Z"}}`
	withVerdicts := func(vs ...Verdict) StoredRecord {
		r := rec("1", 1, "", "", "A")
		r.Statement.Predicate.Verdicts = vs
		r.Statement.Predicate.Intent.CorrelationID = "incident-7"
		r.Statement.Predicate.Intent.Cause = &Cause{Operation: "u0"}
		return r
	}
	pre := Verdict{Gate: "g", Phase: "pre", Citation: map[string]string{"input": input}}
	verify := Verdict{Gate: "g", Phase: "verify", Citation: map[string]string{"input": input, "from": "2026-11-05T12:00:00Z"}}
	for _, c := range []struct {
		name string
		r    StoredRecord
		want string
	}{
		{"verify input", withVerdicts(pre, verify, Verdict{Phase: "verify", Citation: map[string]string{"input": `{"window":{"from":"2026-11-05T11:00:00Z"}}`}}), "2026-11-05T10:00:00Z"},
		{"pre-gate only", withVerdicts(pre), ""},
		{"no input", withVerdicts(Verdict{Phase: "verify", Citation: map[string]string{"from": "2026-11-05T12:00:00Z"}}), ""},
		{"malformed input", withVerdicts(Verdict{Phase: "verify", Citation: map[string]string{"input": "{"}}), ""},
		{"zero window", withVerdicts(Verdict{Phase: "verify", Citation: map[string]string{"input": "{}"}}), ""},
	} {
		e := RecordEntry(c.r)
		if e.Applied != c.want || e.CorrelationID != "incident-7" || e.Cause == nil || e.Cause.Operation != "u0" {
			t.Errorf("%s: entry = %+v", c.name, e)
		}
	}
}

func TestNowOfServiceAndEnvironment(t *testing.T) {
	st := ansStage()
	st.Spec = StageSpec{Service: ServiceRef{Name: "payments", Domain: "d"}, Environment: "prod"}
	if n := NowOf(st); n.Service == nil || *n.Service != st.Spec.Service || n.Environment != "prod" || n.ServiceFrom != "stage" {
		t.Fatalf("now of a Stage with a service = %+v", n)
	}
	// A Stage that could not be read is an empty object: its service is read
	// from the newest record's policy.
	empty := &StageObject{Metadata: ObjectMeta{Name: "payments-prod"}}
	recs := []StoredRecord{
		ansRecord("r1", "u1", "a", 1, ansA, func(p *RecordPredicate) {
			p.Policy = &StageSpec{Service: ServiceRef{Name: "old"}, Environment: "stale"}
		}),
		ansRecord("r2", "u2", "b", 2, ansB, func(p *RecordPredicate) {
			p.Policy = &StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "prod"}
		}),
		ansRecord("r3", "u3", "c", 3, "", nil),
	}
	h, _ := AssembleHistory(empty, nil, &RecordsRead{Fold: Fold{Records: recs}}, nil, "now")
	if n := h.Stage; n.Service == nil || n.Service.Name != "payments" || n.Environment != "prod" || n.ServiceFrom != "record" {
		t.Fatalf("now from the records = %+v", n)
	}
	h, _ = AssembleHistory(empty, nil, &RecordsRead{Fold: Fold{Records: recs[2:]}}, nil, "now")
	if n := h.Stage; n.Service != nil || n.Environment != "" || n.ServiceFrom != "" {
		t.Fatalf("now with no service anywhere = %+v", n)
	}
	h, _ = AssembleHistory(st, nil, &RecordsRead{Fold: Fold{Records: recs[:1]}}, nil, "now")
	if n := h.Stage; n.Service.Name != "payments" || n.ServiceFrom != "stage" {
		t.Fatalf("the Stage's own service wins over the records: %+v", n)
	}
}

// TestRecordPredicateUnchanged pins the signed predicate's JSON: the history
// fields read from a record never change what is signed.
func TestRecordPredicateUnchanged(t *testing.T) {
	r := rec("1", 1, "", "", "A")
	r.Statement.Predicate.Intent.CorrelationID = "incident-7"
	r.Statement.Predicate.Intent.Cause = &Cause{Operation: "u0"}
	r.Statement.Predicate.Verdicts = []Verdict{{Gate: "g", Phase: "verify", Result: Pass, Citation: map[string]string{"input": "{}"}}}
	b, err := json.Marshal(r.Statement.Predicate)
	if err != nil || string(b) != recordPredicateGolden {
		t.Fatalf("predicate json = %s %v", b, err)
	}
}

const recordPredicateGolden = `{"kind":"operation","type":{"id":"delivery.pacto.trianalab.io/promote","version":"","digest":""},"intent":{"uid":"u-1","name":"op-1","stage":{"cluster":"","namespace":"","name":""},"params":{"to":"A"},"correlationID":"incident-7","cause":{"operation":"u0"}},"actor":{"class":"agent","user":"bot"},"verdicts":[{"gate":"g","phase":"verify","result":"Pass","citation":{"input":"{}"}}],"outcome":{"result":"Succeeded","verification":"Verified","pointer":{"after":"A"}},"chain":{"stream":"c/ns/s","seq":1},"time":{"finished":"2026-10-21T10:00:01Z"}}`
