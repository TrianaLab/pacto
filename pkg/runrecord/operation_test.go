package runrecord

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	testUID   = "0f6c2a1e-5b7d-4c3a-9e2f-1a2b3c4d5e6f"
	digestTo  = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	digestOld = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	digestCon = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
)

// goldenInput is a Failed, Unverified promote held 14 minutes.
func goldenInput() OperationInput {
	return OperationInput{
		UID:       testUID,
		Namespace: "payments-prod",
		Spec: delivery.OperationSpec{
			Type: delivery.TypePromote, Stage: "prod", Facet: delivery.FacetPointer,
			Params: delivery.Params{From: digestOld, To: digestTo},
		},
		Status: delivery.OperationStatus{
			Outcome: delivery.Failed, Verification: delivery.Unverified, Reason: "VerifyFailed",
			ActorClass: delivery.ClassHuman,
			Verdicts: []delivery.Verdict{
				{Gate: delivery.GateImpact, Phase: "pre", Result: delivery.Pass, Citation: map[string]string{"contract": "oci://registry.example.com/acme/payments@" + digestCon}},
				{Gate: "pacto.health", Phase: "verify", Result: delivery.Fail},
			},
			Times: delivery.Times{
				Admitted: "2026-11-13T09:00:00Z", Held: "2026-11-13T09:01:00Z",
				Applied: "2026-11-13T09:15:00Z", Finished: "2026-11-13T09:20:00Z",
			},
			Policy: &delivery.StageSpec{Service: delivery.ServiceRef{Domain: "acme", Name: "payments"}, Environment: "prod"},
		},
		ClusterID:    "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
		PactoVersion: "4.6.0",
		RecordedAt:   time.Date(2026, 11, 13, 10, 20, 1, 500, time.FixedZone("x", 3600)),
	}
}

func mustBuild(t *testing.T, in OperationInput) (Record, string) {
	t.Helper()
	r, ok := FromOperation(in)
	if !ok {
		t.Fatal("FromOperation refused the input")
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, string(data)
}

func TestOperationRecordGolden(t *testing.T) {
	r, _ := mustBuild(t, goldenInput())
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/operation.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, bytes.TrimSuffix(data, []byte("\n"))) {
		t.Fatalf("FromOperation does not reproduce the golden:\n%s", out)
	}
	if parsed, err := Parse(data); err != nil || !reflect.DeepEqual(parsed, r) {
		t.Fatalf("the golden does not round-trip: %v", err)
	}
	if r.HeldFor != "14m0s" || r.RecordedAt != time.Date(2026, 11, 13, 9, 20, 1, 0, time.UTC) {
		t.Fatalf("heldFor %q recordedAt %v", r.HeldFor, r.RecordedAt)
	}
}

func TestFromOperationDelegated(t *testing.T) {
	in := goldenInput()
	in.Spec.OnBehalfOf = &delivery.Principal{User: "alice"}
	r, _ := mustBuild(t, in)
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/operation-delegated.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Delegated || !bytes.Equal(out, bytes.TrimSuffix(data, []byte("\n"))) {
		t.Fatalf("a delegated operation does not reproduce the golden:\n%s", out)
	}
	if parsed, err := Parse(data); err != nil || !reflect.DeepEqual(parsed, r) {
		t.Fatalf("the delegated golden does not round-trip: %v", err)
	}
	if _, plain := mustBuild(t, goldenInput()); strings.Contains(plain, `"delegated"`) {
		t.Fatalf("an operation with no onBehalfOf records delegated: %s", plain)
	}
}

func TestFromOperationNoStage(t *testing.T) {
	in := goldenInput()
	in.Status.Policy = nil
	if _, ok := FromOperation(in); ok {
		t.Fatal("built a record for an operation with no Stage")
	}
}

func TestFromOperationTypeIsPathBase(t *testing.T) {
	for _, typ := range delivery.Types() {
		in := goldenInput()
		in.Spec.Type = typ.ID
		r, _ := mustBuild(t, in)
		if want := typ.ID[strings.LastIndex(typ.ID, "/")+1:]; r.Type != want {
			t.Errorf("%s: type %q, want %q", typ.ID, r.Type, want)
		}
	}
	in := goldenInput()
	in.Spec.Type = "delivery.pacto.trianalab.io/provision"
	if r, _ := mustBuild(t, in); r.Type != "provision" {
		t.Fatalf("unknown type maps to %q", r.Type)
	}
}

func TestFromOperationOmitsUnknown(t *testing.T) {
	in := goldenInput()
	in.Status.Times.Held = ""
	in.Status.Verdicts = nil
	in.Spec.Params.From = ""
	_, data := mustBuild(t, in)
	for _, key := range []string{`"heldFor"`, `"contract"`, `"from"`, `"gates"`} {
		if strings.Contains(data, key) {
			t.Errorf("record holds %s: %s", key, data)
		}
	}
}

func TestFromOperationContractFromCitation(t *testing.T) {
	cite := func(ref string) delivery.Verdict {
		return delivery.Verdict{Gate: "g", Phase: "pre", Result: delivery.Pass, Citation: map[string]string{"contract": ref}}
	}
	other := "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	in := goldenInput()
	in.Status.Verdicts = []delivery.Verdict{{Gate: "g", Phase: "pre", Result: delivery.Pass}, cite("not-a-digest"), cite("oci://r/x@" + digestCon), cite("r/x@" + other)}
	in.Status.Plan = &delivery.Plan{Verdicts: []delivery.Verdict{cite("r/x@" + other)}}
	if r, _ := mustBuild(t, in); r.Contract != digestCon {
		t.Fatalf("contract %q, want the first citing verdict", r.Contract)
	}
	in.Status.Verdicts = []delivery.Verdict{cite("r/x@sha512:" + strings.Repeat("a", 128))}
	if r, _ := mustBuild(t, in); r.Contract != other {
		t.Fatalf("contract %q, want the plan's citation", r.Contract)
	}
	in.Status.Plan = nil
	if r, _ := mustBuild(t, in); r.Contract != "" {
		t.Fatalf("contract %q from a non-sha256 citation", r.Contract)
	}
}

func TestHeldForTable(t *testing.T) {
	cases := []struct {
		name string
		t    delivery.Times
		want string
	}{
		{"held then applied", delivery.Times{Held: "2026-11-13T09:00:00Z", Applied: "2026-11-13T09:14:00Z", Finished: "2026-11-13T09:30:00Z"}, "14m0s"},
		{"held after applied", delivery.Times{Applied: "2026-11-13T09:00:00Z", Held: "2026-11-13T09:05:00Z", Finished: "2026-11-13T10:05:00Z"}, "1h0m0s"},
		{"held then expired", delivery.Times{Held: "2026-11-13T09:00:00Z", Finished: "2026-11-13T09:00:30Z"}, "30s"},
		{"not held", delivery.Times{Applied: "2026-11-13T09:00:00Z", Finished: "2026-11-13T09:30:00Z"}, ""},
		{"unparsable held", delivery.Times{Held: "yesterday", Finished: "2026-11-13T09:30:00Z"}, ""},
		{"unparsable finished", delivery.Times{Held: "2026-11-13T09:00:00Z", Finished: "soon"}, ""},
		{"finished before held", delivery.Times{Held: "2026-11-13T09:00:00Z", Finished: "2026-11-13T08:00:00Z"}, ""},
	}
	for _, c := range cases {
		if got := heldFor(c.t); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOperationRecordCarriesNoIdentity(t *testing.T) {
	in := goldenInput()
	in.Spec.Actor = delivery.Actor{User: "alice@example.com", Groups: []string{"alice-team"}}
	in.Spec.OnBehalfOf = &delivery.Principal{User: "alice"}
	in.Spec.CorrelationID = "CHG-1 alice"
	in.Spec.Reason = "alice asked"
	in.Status.Approvals = []delivery.ApprovalRecord{{Name: "a", By: "bob", Decision: delivery.Reject, Note: "no"}}
	in.Status.Message = "RejectedByHuman: bob: no"
	in.Status.Question = "bob, may alice ship?"
	in.Status.Verdicts[1].Message = "bob broke it"
	in.Status.Verdicts[1].Citation = map[string]string{"by": "alice"}
	in.Status.Reason = "RejectedByHuman"
	forbidden := []string{"alice", "bob", "carol", "CHG-1", `no"`}
	r, data := mustBuild(t, in)
	if !r.Delegated {
		t.Fatalf("onBehalfOf is not recorded as delegated: %s", data)
	}
	for _, s := range forbidden {
		if strings.Contains(data, s) {
			t.Errorf("record holds %q: %s", s, data)
		}
	}
	in.Status.Reason = "carol rejected this"
	_, data = mustBuild(t, in)
	if strings.Contains(data, `"reason"`) || strings.Contains(data, "carol") {
		t.Fatalf("a reason with no code was copied: %s", data)
	}
}

func TestFromOperationDropsNonDigestTo(t *testing.T) {
	in := goldenInput()
	in.Spec.Params = delivery.Params{To: "alice"}
	_, data := mustBuild(t, in)
	in = goldenInput()
	in.Spec.Type, in.Spec.Facet = delivery.TypeAutonomy, "level"
	in.Spec.Params = delivery.Params{From: "advise", To: "autonomous"}
	_, level := mustBuild(t, in)
	in.Spec.Params = delivery.Params{From: digestOld, To: digestTo}
	_, levelDigests := mustBuild(t, in)
	for _, d := range []string{data, level, levelDigests} {
		if strings.Contains(d, `"to"`) || strings.Contains(d, `"from"`) {
			t.Errorf("record holds to or from: %s", d)
		}
	}
}

func TestFromOperationServiceDomain(t *testing.T) {
	if r, _ := mustBuild(t, goldenInput()); r.ServiceDomain != "acme" {
		t.Fatalf("serviceDomain %q", r.ServiceDomain)
	}
	in := goldenInput()
	in.Status.Policy.Service.Domain = ""
	if _, data := mustBuild(t, in); strings.Contains(data, `"serviceDomain"`) {
		t.Fatalf("empty domain recorded: %s", data)
	}
}

func TestOperationRecordIDIsUID(t *testing.T) {
	a, _ := mustBuild(t, goldenInput())
	in := goldenInput()
	in.RecordedAt = in.RecordedAt.Add(time.Hour)
	b, _ := mustBuild(t, in)
	if a.ID != testUID || b.ID != a.ID {
		t.Fatalf("ids %q %q, want the uid", a.ID, b.ID)
	}
}

func TestFromOperationEncodes(t *testing.T) {
	var inputs []OperationInput
	for _, typ := range delivery.Types() {
		in := goldenInput()
		in.Spec.Type, in.Spec.Facet = typ.ID, typ.Facet
		inputs = append(inputs, in)
	}
	autonomy := goldenInput()
	autonomy.Spec.Type, autonomy.Spec.Facet = delivery.TypeAutonomy, "level"
	autonomy.Spec.Params = delivery.Params{From: "advise", To: "autonomous"}
	ack := goldenInput()
	ack.Spec.Type, ack.Spec.Facet = delivery.TypeAcknowledge, "upstream"
	ack.Spec.Params = delivery.Params{Record: digestOld}
	rejected := goldenInput()
	rejected.Spec.Params = delivery.Params{To: "alice"}
	rejected.Status = delivery.OperationStatus{Outcome: delivery.Rejected, Reason: "Bad reason: alice",
		Times: delivery.Times{Finished: "2026-11-13T09:00:00Z"}, Policy: rejected.Status.Policy}
	for _, in := range append(inputs, autonomy, ack, rejected) {
		r, _ := mustBuild(t, in)
		data, err := Encode(r)
		if err != nil {
			t.Errorf("%s: %v", in.Spec.Type, err)
			continue
		}
		if _, err := Parse(data); err != nil {
			t.Errorf("%s: Parse refuses Encode's output: %v", in.Spec.Type, err)
		}
	}
}

func operationGolden(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/operation.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func mustReject(t *testing.T, name, data, field string) {
	t.Helper()
	_, err := Parse([]byte(data))
	if err == nil {
		t.Errorf("%s: parsed", name)
		return
	}
	if !strings.Contains(err.Error(), field) {
		t.Errorf("%s: the error does not name %s: %v", name, field, err)
	}
}

func TestParseOperationRejectsUnknownField(t *testing.T) {
	g := operationGolden(t)
	if _, err := Parse([]byte(g)); err != nil {
		t.Fatal(err)
	}
	mustReject(t, "by", strings.Replace(g, `"kind"`, `"by":"x","kind"`, 1), "by")
	mustReject(t, "gate message", strings.Replace(g, `"result":"Fail"`, `"result":"Fail","message":"x"`, 1), "message")
	mustReject(t, "stage user", strings.Replace(g, `"name":"prod"`, `"name":"prod","user":"x"`, 1), "user")
}

func TestParseOperationRequiresStage(t *testing.T) {
	g := operationGolden(t)
	for _, f := range []string{"namespace", "name", "cluster"} {
		var m map[string]any
		if err := json.Unmarshal([]byte(g), &m); err != nil {
			t.Fatal(err)
		}
		delete(m["stage"].(map[string]any), f)
		data, _ := json.Marshal(m)
		mustReject(t, f, string(data), "stage."+f)
	}
	mustReject(t, "no stage", strings.Replace(g, `"stage":{"namespace":"payments-prod","name":"prod","cluster":"9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"},`, "", 1), "stage")
}

func TestParseOperationRejects(t *testing.T) {
	g := operationGolden(t)
	cases := map[string][2]string{
		"type":         {`"type":"promote"`, `"type":"Promote Me"`},
		"no type":      {`"type":"promote",`, ``},
		"verification": {`"verification":"Unverified"`, `"verification":"Maybe"`},
		"actorClass":   {`"actorClass":"human"`, `"actorClass":"alice"`},
		"heldFor":      {`"heldFor":"14m0s"`, `"heldFor":"-1m"`},
		"to":           {`"to":"` + digestTo + `"`, `"to":"alice"`},
		"from":         {`"from":"` + digestOld + `"`, `"from":"advise"`},
		"contract":     {`"contract":"` + digestCon + `"`, `"contract":"sha256:x"`},
		"gates":        {`"result":"Fail"`, `"result":"Bad"`},
		"finishedAt":   {`"finishedAt":"2026-11-13T09:20:00Z"`, `"finishedAt":"later"`},
		"startedAt":    {`"startedAt":"2026-11-13T09:00:00Z"`, `"startedAt":"earlier"`},
		"service":      {`"service":"payments"`, `"service":""`},
	}
	for field, c := range cases {
		mustReject(t, field, strings.Replace(g, c[0], c[1], 1), strings.TrimPrefix(field, "no "))
	}
	mustReject(t, "no finishedAt", strings.Replace(g, `,"finishedAt":"2026-11-13T09:20:00Z"`, ``, 1), "finishedAt")
	mustReject(t, "gate phase", strings.Replace(g, `"phase":"verify"`, `"phase":"later"`, 1), "gates")
	mustReject(t, "gate name", strings.Replace(g, `"gate":"pacto.health"`, `"gate":""`, 1), "gates")
}

func TestParseOperationRejectsBadReason(t *testing.T) {
	g := operationGolden(t)
	for _, reason := range []string{"RejectedByHuman: bob", "bob", strings.Repeat("A", 65)} {
		mustReject(t, reason, strings.Replace(g, `"reason":"VerifyFailed"`, `"reason":"`+reason+`"`, 1), "reason")
	}
}

func TestParseOperationRejectsCIFields(t *testing.T) {
	g := operationGolden(t)
	for _, kv := range []string{
		`"oldRevision":"` + digestOld + `"`, `"newRevision":"` + digestTo + `"`, `"bundleDigest":"` + digestTo + `"`,
		`"deployDigest":"` + digestTo + `"`, `"classification":"BREAKING"`, `"gate":{"verdict":"clear"}`,
		`"consumers":{"incompatible":0,"unknown":0,"compatible":0,"unclassified":0,"blocking":0},"completeness":"complete","asOf":"2026-11-13T09:00:00Z"`,
		`"repository":"acme/payments"`, `"ciRunUrl":"https://ci.example.com/1"`,
	} {
		field := strings.Trim(strings.SplitN(kv, ":", 2)[0], `"`)
		mustReject(t, field, strings.Replace(g, `"kind"`, kv+`,"kind"`, 1), field)
	}
}

func TestParseCIRejectsOperationFields(t *testing.T) {
	g := golden(t)
	for _, kv := range []string{
		`"type":"promote"`, `"verification":"Verified"`, `"reason":"VerifyFailed"`, `"actorClass":"human"`,
		`"heldFor":"1m0s"`, `"serviceDomain":"acme"`, `"stage":{"namespace":"n","name":"s","cluster":"c"}`,
		`"to":"` + digestTo + `"`, `"from":"` + digestOld + `"`, `"contract":"` + digestCon + `"`,
		`"gates":[{"gate":"g","phase":"pre","result":"Pass"}]`, `"startedAt":"2026-11-13T09:00:00Z"`, `"finishedAt":"2026-11-13T09:00:00Z"`,
		`"delegated":true`,
	} {
		field := strings.Trim(strings.SplitN(kv, ":", 2)[0], `"`)
		mustReject(t, field, strings.Replace(g, `"kind"`, kv+`,"kind"`, 1), field)
	}
}

func TestParseOperationOutcomeList(t *testing.T) {
	g, ci := operationGolden(t), golden(t)
	for _, o := range []delivery.Outcome{delivery.Succeeded, delivery.Failed, delivery.Errored, delivery.Cancelled,
		delivery.Expired, delivery.Rejected, delivery.Superseded, delivery.Witnessed} {
		if _, err := Parse([]byte(strings.Replace(g, `"outcome":"Failed"`, `"outcome":"`+string(o)+`"`, 1))); err != nil {
			t.Errorf("%s refused for operation: %v", o, err)
		}
		mustReject(t, string(o), strings.Replace(ci, `"outcome":"failed"`, `"outcome":"`+string(o)+`"`, 1), "outcome")
	}
	mustReject(t, "passed", strings.Replace(g, `"outcome":"Failed"`, `"outcome":"passed"`, 1), "outcome")
}

func TestParseOperationIDIsUID(t *testing.T) {
	g, ci := operationGolden(t), golden(t)
	mustReject(t, "sha256 id", strings.Replace(g, `"id":"`+testUID+`"`, `"id":"`+digestTo+`"`, 1), "id")
	diff := strings.Replace(ci, `"kind":"impact"`, `"kind":"diff"`, 1)
	mustReject(t, "uid id", strings.Replace(diff, `"id":"sha256:c1714d93c4049e5b8aad22a64ab1682e207731ffac9adef7711123b204194894"`, `"id":"`+testUID+`"`, 1), "id")
}
