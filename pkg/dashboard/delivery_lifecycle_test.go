package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func lifecycleFake() *fakeDelivery {
	f := sampleCluster("acme:team-payments")
	f.contract = "oci://r/payments@" + dContract
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "anything"}, Completeness: "complete", Source: delivery.SourceRecords, Verified: true,
		Entries: []delivery.HistoryEntry{{Seq: 1, Operation: "ship", Type: delivery.TypePromote, From: dOld, To: dRun, Record: "sha256:r1",
			Outcome: delivery.Succeeded, Verification: delivery.Verified, Pointer: &delivery.PointerChange{Before: dOld, After: dRun}}}}
	return f
}

func TestDeliveryLifecycle(t *testing.T) {
	f := lifecycleFake()
	fl := revisionFleet(t, contractRev("payments", dContract))
	base := startDeliveryWith(t, f, Options{Fleet: fl, Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true)}, true, nil)
	code, b := call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&version=1.0.0", nil)
	expect(t, code == http.StatusOK && b["digest"] == dRun && b["version"] == "1.0.0" && b["service"] == "payments" &&
		b["signature"] == "signed" && b["completeness"] == "complete" && b["asOf"] != "", "lifecycle %d %v", code, b)
	expect(t, b["revisions"].(map[string]any)[dRun] == revisionKeyOf(t, fl, "payments") && b["versions"].(map[string]any)[dRun] == "1.0.0",
		"labels %v %v", b["revisions"], b["versions"])
	stages := b["stages"].([]any)
	expect(t, len(stages) == 1, "only payments' Stages: %v", stages)
	s := stages[0].(map[string]any)
	ev := s["events"].([]any)
	expect(t, s["stage"] == "payments" && s["running"] == true && s["verified"] == "yes" && len(ev) == 1 &&
		ev[0].(map[string]any)["role"] == "arrived" && ev[0].(map[string]any)["operation"] == "ship", "stage %v", s)
	expectLifecycleDegraded(t, f, base)
}

// expectLifecycleDegraded drives the partial, invalid and not-installed answers.
func expectLifecycleDegraded(t *testing.T, f *fakeDelivery, base string) {
	t.Helper()
	f.historyErr = errors.New("records unreadable")
	_, b := call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&digest="+dRun, nil)
	expect(t, b["completeness"] == "partial" && strings.Contains(fmt.Sprint(b["unreadable"]), "prod/payments: records unreadable"), "history error %v", b)
	f.historyErr = nil

	f.forbid = map[string]bool{"": true}
	f.forbidAll, f.stageNS = true, []string{"prod", "books"}
	f.forbid["books"] = true
	_, b = call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&digest="+dRun, nil)
	expect(t, b["completeness"] == "partial" && fmt.Sprint(b["unreadable"]) == "[books]", "forbidden namespace %v", b)

	code, _ := call(t, http.MethodGet, base+"/api/delivery/lifecycle?digest="+dRun, nil)
	expect(t, code == http.StatusUnprocessableEntity, "lifecycle needs a service: %d", code)
	f.listErr = errors.New("apiserver down")
	code, _ = call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&digest="+dRun, nil)
	expect(t, code >= 500, "a list that fails: %d", code)

	off := startDeliveryWith(t, f, Options{Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true)}, false, nil)
	code, b = call(t, http.MethodGet, off+"/api/delivery/lifecycle?service=payments&digest="+dRun, nil)
	expect(t, code == http.StatusNotFound && b["code"] == "DeliveryNotInstalled", "not installed: %d %v", code, b)
}

func TestDeliveryLifecycleAmbiguous(t *testing.T) {
	f := lifecycleFake()
	f.known = map[string]string{dNext: "1.0.0"}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	for url, want := range map[string]string{
		"service=payments&version=1.0.0":                "version 1.0.0 names 2 artifacts",
		"service=payments":                              "pass exactly one of digest, version or contract",
		"service=payments&version=1.0.0&digest=" + dRun: "pass exactly one",
		"service=payments&digest=latest":                "digest latest is not an artifact digest",
	} {
		code, b := call(t, http.MethodGet, base+"/api/delivery/lifecycle?"+url, nil)
		expect(t, code == http.StatusBadRequest && strings.Contains(fmt.Sprint(b["detail"]), want), "%s: %d %v", url, code, b)
	}
}

// TestDeliveryLifecycleNotFound: a miss is LifecycleNotFound only when the
// search read everything; a partial Stage listing makes it unknown.
func TestDeliveryLifecycleNotFound(t *testing.T) {
	f := lifecycleFake()
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, b := call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&version=9.9.9", nil)
	expect(t, code == http.StatusNotFound && b["code"] == "LifecycleNotFound", "a definite miss: %d %v", code, b)
	f.forbid = map[string]bool{"": true}
	f.forbidAll, f.stageNS = true, []string{"prod", "books"}
	f.forbid["books"] = true
	code, b = call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&version=9.9.9", nil)
	expect(t, code == http.StatusBadRequest && b["code"] == "LifecycleUnresolved" && strings.Contains(fmt.Sprint(b["detail"]), "Stages not listed: books"),
		"a miss beside an unlisted namespace: %d %v", code, b)
}

func TestDeliveryLifecycleDigestLimit(t *testing.T) {
	many := func(n int, p string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("sha256:%s%063d", p, i)
		}
		return out
	}
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{
		stage("prod", "a", "payments", delivery.StageStatus{Published: many(50, "a"), Offered: many(50, "b"), Verified: many(10, "c"), FailedDigests: many(50, "d")}),
		stage("test", "b", "payments", delivery.StageStatus{Published: many(50, "e"), Offered: many(50, "f"), Pointer: dRun}),
	}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, b := call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&version=1.0.0", nil)
	expect(t, code == http.StatusBadRequest && b["code"] == "LifecycleUnresolved" &&
		strings.Contains(fmt.Sprint(b["detail"]), "only the first 200 were read to find the one asked for: it may be among those"),
		"the 261st artifact is not read, so the miss is unknown: %d %v", code, b)
	f.stages[0].Status.Pointer = dRun
	code, b = call(t, http.MethodGet, base+"/api/delivery/lifecycle?service=payments&version=1.0.0", nil)
	expect(t, code == http.StatusOK && b["digest"] == dRun && b["completeness"] == "partial" &&
		strings.Contains(b["note"].(string), "version 1.0.0 names one artifact Pacto read, but the Stages know 261 artifacts and only the first 200 were read"),
		"cap note %d %v", code, b)
}

// TestLifecycleSignatureMatchesPublished: the Publish page and the lifecycle
// answer give one digest the same signature.
func TestLifecycleSignatureMatchesPublished(t *testing.T) {
	full := make([]string, delivery.MaxPublished)
	for i := range full {
		full[i] = fmt.Sprintf("sha256:%064d", i+1)
	}
	oci := delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "oci"}}
	at := func(ns string, c delivery.Candidates, s delivery.StageStatus) delivery.StageObject {
		st := stage(ns, "payments", "payments", s)
		st.Spec.Candidates = c
		return st
	}
	stages := []delivery.StageObject{
		at("test", oci, delivery.StageStatus{Pointer: dRun, Published: []string{dNext}}),
		at("prod", delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments", Namespace: "test"}}, delivery.StageStatus{Offered: []string{dNext}, Pointer: dOld}),
		at("edge", oci, delivery.StageStatus{Pointer: dOld, Published: full}),
	}
	byService, _ := publishedDigests(stages, "", "", "")
	for _, d := range []string{dRun, dNext, dOld} {
		want := byService[publishedKey{Service: "payments"}][d].Signature
		if got := delivery.LifecycleOf(delivery.Revision{Digest: d}, stages, nil).Signature; got != want {
			t.Errorf("%s: lifecycle %s, publish %s", d, got, want)
		}
	}
}
