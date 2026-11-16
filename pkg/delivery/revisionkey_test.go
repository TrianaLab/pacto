package delivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	rkContract = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	rkArtA     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rkArtB     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func stageOf(service, domain string) StageObject {
	var st StageObject
	st.Spec.Service = ServiceRef{Name: service, Domain: domain}
	return st
}

func TestRecordEntryCarriesImpactVerdict(t *testing.T) {
	r := rec("1", 1, "", "", "A")
	r.Statement.Predicate.Verdicts = []Verdict{{Gate: GateContract, Result: Pass},
		{Gate: GateImpact, Phase: "pre", Result: Fail, Message: "breaks payments", Citation: map[string]string{"k": "v"}}}
	e := RecordEntry(r)
	if e.Impact == nil || e.Impact.Result != Fail || e.Impact.Message != "breaks payments" {
		t.Fatalf("impact = %+v", e.Impact)
	}
	// A copy: editing the line never edits the record.
	e.Impact.Citation["k"] = "x"
	e.Impact.Result = Pass
	if v := r.Statement.Predicate.Verdicts[1]; v.Result != Fail {
		t.Fatalf("record verdict changed: %+v", v)
	}
	if e := RecordEntry(rec("2", 2, "1", "A", "B")); e.Impact != nil {
		t.Fatalf("a record with no impact gate has impact %+v", e.Impact)
	}
}

func TestStatusLineHasNoImpact(t *testing.T) {
	var o OperationObject
	o.Status.Verdicts = []Verdict{{Gate: GateImpact, Result: Pass}}
	if e := StatusEntry(o); e.Impact != nil {
		t.Fatalf("status line impact = %+v", e.Impact)
	}
	if b, _ := json.Marshal(StatusEntry(o)); strings.Contains(string(b), `"impact"`) {
		t.Fatalf("status line json = %s", b)
	}
}

func TestSignedMove(t *testing.T) {
	signed := RecordEntry(rec("1", 1, "", "", "A"))
	if !SignedMove(History{Verified: true}, signed) {
		t.Fatal("a record on a verified history is a signed move")
	}
	if SignedMove(History{Source: SourceRecords}, signed) {
		t.Fatal("a record on an unverified history is not a signed move")
	}
	var o OperationObject
	o.Metadata.UID = "u"
	o.Status.Pointer = PointerChange{After: "A"}
	o.Status.Outcome = Succeeded
	if SignedMove(History{Verified: true}, StatusEntry(o)) {
		t.Fatal("a status line is not a signed move")
	}
	u := rec("2", 2, "1", "", "")
	u.Statement.Predicate.Kind, u.Statement.Predicate.Outcome = RecordUnrecordable, nil
	u.Statement.Predicate.Unrecordable = &Refusal{Check: CheckApproval}
	u.Statement.Predicate.Intent.UID = "u"
	e := WithUnsignedMove(RecordEntry(u), []OperationObject{o}, "u")
	if e.Record == "" || e.Pointer == nil || SignedMove(History{Verified: true}, e) {
		t.Fatalf("an unrecordable move is not signed: %+v", e)
	}
}

func TestDescribedOfFields(t *testing.T) {
	r := Revision{Digest: rkArtA, ContractRef: "oci://ghcr.io/trianalab/pacto/orders-service@" + rkContract, SourceRevision: "abc", Err: "",
		Bundle: &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "orders-service", Version: "1.2.0",
			Domain: "commerce", Owner: contract.Owner{Team: "commerce-team"}}}}}
	d := DescribedOf(r, "1.2.0")
	want := Described{Version: "1.2.0", Team: r.Bundle.Contract.Service.Owner.DisplayString(), Contract: r.ContractRef, Source: "abc",
		Service: "orders-service", BusinessDomain: "commerce", OwnerKey: r.Bundle.Contract.Service.Owner.KeyString()}
	if d != want {
		t.Fatalf("described = %+v, want %+v", d, want)
	}
	if d := DescribedOf(Revision{ContractRef: "x", Err: "gone"}, ""); d != (Described{Contract: "x", Err: "gone"}) {
		t.Fatalf("unread described = %+v", d)
	}
}

func TestRevisionKeyOfRegistryKey(t *testing.T) {
	ref := "oci://ghcr.io/trianalab/pacto/orders-service@" + rkContract
	got := RevisionKeyOf(stageOf("orders-service", "ghcr.io/trianalab/pacto"), Described{Contract: ref, Service: "orders-service"})
	want := fleet.NewRevisionKey(fleet.NewServiceKeyDomain(contract.OCIDomain(ref), "orders-service"), rkContract)
	if got != want || got == "" {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestRevisionKeyOfDefaultDomainStage(t *testing.T) {
	ref := "oci://ghcr.io/trianalab/pacto/orders-service@" + rkContract
	got := RevisionKeyOf(stageOf("orders-service", ""), Described{Contract: ref, Service: "orders-service"})
	want := fleet.NewRevisionKey(fleet.NewServiceKeyDomain("ghcr.io/trianalab/pacto", "orders-service"), rkContract)
	if got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestRevisionKeyOfUnscopedStage(t *testing.T) {
	// A contract with no repository path keys its service with no domain.
	got := RevisionKeyOf(stageOf("orders", ""), Described{Contract: "orders@" + rkContract, Service: "orders"})
	if want := fleet.NewRevisionKey(fleet.NewServiceKey("orders"), rkContract); got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestRevisionKeyOfMismatchedServiceEmpty(t *testing.T) {
	d := Described{Contract: "oci://reg/pactos/payments@" + rkContract, Service: "payments"}
	if got := RevisionKeyOf(stageOf("orders", ""), d); got != "" {
		t.Fatalf("a bundle of another service got key %q", got)
	}
}

func TestRevisionKeyOfUnreadEmpty(t *testing.T) {
	st := stageOf("payments", "")
	for _, d := range []Described{
		{Contract: "oci://reg/pactos/payments@" + rkContract, Err: "unreachable", Service: "payments"},
		{Contract: "oci://reg/pactos/payments@" + rkContract},            // no bundle read
		{Contract: "oci://reg/pactos/payments:1.0", Service: "payments"}, // not digest-pinned
	} {
		if got := RevisionKeyOf(st, d); got != "" {
			t.Fatalf("RevisionKeyOf(%+v) = %q, want none", d, got)
		}
	}
}

func TestHistoryDigests(t *testing.T) {
	h := History{Stage: StageNow{Pointer: rkArtA}, Entries: []HistoryEntry{
		{From: "supervised", To: "autonomous"},
		{From: rkArtA, To: rkArtB, Pointer: &PointerChange{Before: rkArtA, After: rkArtB}},
	}}
	got := HistoryDigests(h)
	if strings.Join(got, ",") != rkArtA+","+rkArtB {
		t.Fatalf("digests = %v", got)
	}
}

func TestHistoryJSONKeepsFields(t *testing.T) {
	e := RecordEntry(rec("1", 1, "", "", "A"))
	e.Impact = &Verdict{Gate: GateImpact, Result: Pass}
	h := History{Entries: []HistoryEntry{e}}
	b, err := json.Marshal(HistoryAnswer{History: &h, Revisions: map[string]string{"A": "svc@sha"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"stage":`, `"asOf":`, `"completeness":`, `"note":`, `"source":`, `"entries":`, `"seq":`, `"operation":`,
		`"type":`, `"to":`, `"actor":`, `"record":`, `"pointer":`, `"approvals":`, `"impact":`, `"revisions":{"A":"svc@sha"}`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("history json lacks %s: %s", k, b)
		}
	}
	bl := Blame{Digest: "A"}
	b, _ = json.Marshal(BlameAnswer{Blame: &bl, Revision: "svc@sha"})
	if !strings.Contains(string(b), `"digest":"A"`) || !strings.Contains(string(b), `"revision":"svc@sha"`) {
		t.Fatalf("blame json = %s", b)
	}
}
