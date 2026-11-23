package delivery

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSignAndOpenRefusal(t *testing.T) {
	r := PublishRefusal{Service: "payments", Version: "2.4.3", Baseline: "2.4.2", Classification: "BREAKING",
		RequiredBump: "major", Message: "BREAKING since 2.4.2 needs a major bump", At: "2026-10-22T10:00:00Z"}
	st := NewRefusalStatement("sha256:abc", r)
	if len(st.Subject) != 1 || st.Subject[0].Name != "contract" || st.Subject[0].Digest["sha256"] != "abc" {
		t.Fatalf("subject = %+v", st.Subject)
	}
	b, id, err := SignRefusal(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenRefusal(b, func([]byte) error { return nil })
	if err != nil || gotID != id || got.Predicate != r {
		t.Fatalf("open = %+v %s %v", got, gotID, err)
	}
	if _, _, err := OpenRefusal(b, func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an untrusted refusal opened")
	}
	// A record is not a refusal, and a refusal is not a record.
	rec, _, _ := SignRecord(t.Context(), plainSigner{}, Statement{Type: StatementType, PredicateType: PredicateOperation})
	if _, _, err := OpenRefusal(rec, func([]byte) error { return nil }); err == nil {
		t.Fatal("a record opened as a refusal")
	}
	if _, _, err := OpenRecord(b, accept); err == nil {
		t.Fatal("a refusal opened as a record")
	}
}

// manifestsRejected is a promote of payments to sha256:to on Stage prod/prod
// that the manifest inspector rejected with refused.
func manifestsRejected(refused string) OperationObject {
	return OperationObject{Metadata: ObjectMeta{Name: "p1", Namespace: "prod", UID: "u1"},
		Spec: OperationSpec{Type: TypePromote, Stage: "prod", Params: Params{From: "sha256:from", To: "sha256:to"}},
		Status: OperationStatus{Outcome: Rejected, Plan: &Plan{ToVersion: "2.4.3"}, Times: Times{Finished: "2026-11-22T10:00:00Z"},
			Verdicts: []Verdict{{Gate: GatePublished, Result: Pass},
				{Gate: GateManifests, Result: Fail, Citation: map[string]string{"refused": refused}, Message: "the version reaches outside prod: " + refused}}}}
}

func TestApplyRefusalRoundTrip(t *testing.T) {
	svc := ServiceRef{Name: "payments", Domain: "pay/ments"}
	r, ok := ApplyRefusalOf(svc, manifestsRejected("ClusterRoleBinding admin is cluster-scoped; Namespace x is cluster-scoped"))
	want := ApplyRefusal{Service: svc, Stage: "prod", Namespace: "prod", Version: "2.4.3", Digest: "sha256:to", Operation: "p1", At: "2026-11-22T10:00:00Z",
		Refused: []string{"ClusterRoleBinding admin is cluster-scoped", "Namespace x is cluster-scoped"},
		Message: "the version reaches outside prod: ClusterRoleBinding admin is cluster-scoped; Namespace x is cluster-scoped"}
	if !ok || !reflect.DeepEqual(r, want) {
		t.Fatalf("refusal = %+v %v", r, ok)
	}
	r.Record = "rec-id"
	st := NewApplyRefusalStatement(r.Digest, r)
	if len(st.Subject) != 1 || st.Subject[0].Name != "to" || st.Subject[0].Digest["sha256"] != "to" || st.PredicateType != PredicateApplyRefusal {
		t.Fatalf("statement = %+v", st)
	}
	b, id, err := SignApplyRefusal(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenApplyRefusal(b, func([]byte) error { return nil })
	if err != nil || gotID != id || !reflect.DeepEqual(got.Predicate, r) {
		t.Fatalf("open = %+v %s %v", got, gotID, err)
	}
	testApplyRefusalRefusesOthers(t, b)
}

// testApplyRefusalRefusesOthers: an apply refusal b opens only as one, and
// only when verify accepts it.
func testApplyRefusalRefusesOthers(t *testing.T, b []byte) {
	t.Helper()
	if _, _, err := OpenApplyRefusal(b, func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an untrusted refusal opened")
	}
	if _, _, err := OpenRefusal(b, func([]byte) error { return nil }); err == nil {
		t.Fatal("an apply refusal opened as a publish refusal")
	}
	if _, _, err := OpenApplyRefusal([]byte("nope"), func([]byte) error { return nil }); err == nil {
		t.Fatal("garbage opened")
	}
}

func TestApplyRefusalStream(t *testing.T) {
	if s := ApplyRefusalStream(ServiceRef{Name: "payments", Domain: "pay/ments"}); s != "apply-refusals/pay%2Fments/payments" {
		t.Fatalf("stream %s", s)
	}
	if s := ApplyRefusalStream(ServiceRef{Name: "payments"}); s != "apply-refusals/-/payments" {
		t.Fatalf("stream %s", s)
	}
}

func TestApplyRefusalOfOnlyOnManifestsFail(t *testing.T) {
	svc := ServiceRef{Name: "payments"}
	for name, edit := range map[string]func(*OperationObject){
		"succeeded":       func(o *OperationObject) { o.Status.Outcome = Succeeded },
		"another gate":    func(o *OperationObject) { o.Status.Verdicts[1].Result = Unknown },
		"no manifests":    func(o *OperationObject) { o.Status.Verdicts = o.Status.Verdicts[:1] },
		"a rollback":      func(o *OperationObject) { o.Spec.Type = TypeRollback },
		"no target":       func(o *OperationObject) { o.Spec.Params.To = "" },
		"nothing refused": func(o *OperationObject) { o.Status.Verdicts[1].Citation = nil },
	} {
		o := manifestsRejected("ClusterRoleBinding admin is cluster-scoped")
		edit(&o)
		if _, ok := ApplyRefusalOf(svc, o); ok {
			t.Fatalf("%s: refused", name)
		}
	}
	o := manifestsRejected("ClusterRoleBinding admin is cluster-scoped")
	o.Status.Plan = nil
	if r, ok := ApplyRefusalOf(svc, o); !ok || r.Version != "" {
		t.Fatalf("no plan: %+v %v", r, ok)
	}
}

func TestApplyRefusalOfBoundsObjects(t *testing.T) {
	why := make([]string, 60)
	for i := range why {
		why[i] = fmt.Sprintf("Namespace n%d is cluster-scoped", i)
	}
	r, ok := ApplyRefusalOf(ServiceRef{Name: "payments"}, manifestsRejected(strings.Join(why, "; ")))
	if !ok || len(r.Refused) != MaxListedObjects || r.Refused[49] != why[49] {
		t.Fatalf("refused %d %v", len(r.Refused), ok)
	}
}
