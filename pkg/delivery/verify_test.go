package delivery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func pactoAt(name, contractDigest, status string, gen int64) PactoView {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name = "test", name
	p.Status.ContractStatus = status
	p.Status.Contract = &ContractInfoView{ServiceName: "payments"}
	p.Status.EvaluatedRevision = &EvaluatedView{ContractDigest: contractDigest,
		Workload: WorkloadView{Kind: "Deployment", Name: "payments", UID: "w1", Generation: gen}}
	return p
}

func live(uid string, gen, observed int64) func(string, string) *WorkloadState {
	return func(string, string) *WorkloadState {
		return &WorkloadState{UID: uid, Generation: gen, ObservedGeneration: observed}
	}
}

func TestContractVerdict(t *testing.T) {
	ref := "oci://r/pactos/payments@" + c2
	check := func(p []PactoView, applied bool, w func(string, string) *WorkloadState) Verdict {
		return ContractVerdict(ContractCheck{ContractRef: ref, Applied: applied, Pactos: p, Workload: w})
	}
	cases := []struct {
		name string
		v    Verdict
		want Result
		msg  string
	}{
		{"compliant", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, true, live("w1", 2, 2)), Pass, "Compliant"},
		{"warning", check([]PactoView{pactoAt("p", c2, "Warning", 2)}, true, live("w1", 2, 2)), Pass, "Warning"},
		{"noncompliant", check([]PactoView{pactoAt("p", c2, "NonCompliant", 2)}, true, live("w1", 2, 2)), Fail, "NonCompliant"},
		{"invalid", check([]PactoView{pactoAt("p", c2, "Invalid", 2)}, true, live("w1", 2, 2)), Fail, "Invalid"},
		{"unrecognised", check([]PactoView{pactoAt("p", c2, "Reference", 2)}, true, live("w1", 2, 2)), Unknown, "Reference"},
		{"old contract", check([]PactoView{pactoAt("p", c1, "Compliant", 2)}, true, live("w1", 2, 2)), Unknown, "last evaluated contract"},
		{"never evaluated", check([]PactoView{{Status: PactoStatusView{ContractStatus: "Compliant"}}}, true, live("w1", 2, 2)), Unknown, "last evaluated contract (none)"},
		{"not applied", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, false, live("w1", 2, 2)), Unknown, "not confirmed applied"},
		{"no pacto", check(nil, true, live("w1", 2, 2)), Unknown, "no Pacto"},
		{"workload gone", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, true, func(string, string) *WorkloadState { return nil }), Unknown, "gone"},
		{"workload replaced", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, true, live("w2", 2, 2)), Unknown, "replaced"},
		{"not observed", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, true, live("w1", 3, 2)), Unknown, "not observed generation 3"},
		{"stale generation", check([]PactoView{pactoAt("p", c2, "Compliant", 2)}, true, live("w1", 3, 3)), Unknown, "evaluated generation 2"},
		{"fail beats unknown", check([]PactoView{pactoAt("a", c1, "Compliant", 2), pactoAt("b", c2, "NonCompliant", 2)}, true, live("w1", 2, 2)), Fail, "b: NonCompliant"},
	}
	for _, c := range cases {
		if c.v.Result != c.want || !strings.Contains(c.v.Message, c.msg) || c.v.Gate != GateContract || c.v.Phase != "verify" {
			t.Errorf("%s: got %+v, want %s containing %q", c.name, c.v, c.want, c.msg)
		}
	}
	if v := ContractVerdict(ContractCheck{ContractRef: "oci://r/p:1.0.0", Applied: true}); v.Result != Unknown || !strings.Contains(v.Message, "digest-pinned") {
		t.Errorf("tag contract: %+v", v)
	}
	why := ContractVerdict(ContractCheck{ContractRef: ref, AppliedWhy: "listing Kustomizations: forbidden"})
	if why.Result != Unknown || why.Message != "the version is not confirmed applied: listing Kustomizations: forbidden" {
		t.Errorf("the reason it is not applied must reach the verdict, whichever tool applies: %+v", why)
	}
	noWorkload := pactoAt("p", c2, "Compliant", 0)
	noWorkload.Status.EvaluatedRevision.Workload = WorkloadView{}
	if v := check([]PactoView{noWorkload}, true, nil); v.Result != Pass {
		t.Errorf("a reference Pacto with no workload: %+v", v)
	}
}

func TestSnapshotAndViews(t *testing.T) {
	data := []byte(`{"items":[{"metadata":{"namespace":"prod","name":"checkout","generation":3},"status":{"contractStatus":"Compliant","observedGeneration":3,
	  "contract":{"serviceName":"checkout","version":"1.0.0","resolvedRef":"oci://r/pactos/checkout@` + c3 + `"},
	  "dependencies":[{"name":"payments","ref":"oci://r/pactos/payments","required":true,"compatibility":"^1.0.0"}],
	  "evaluatedRevision":{"contractDigest":"` + c3 + `","workload":{"kind":"Deployment","name":"checkout","uid":"u","generation":2}}}}]}`)
	ps, err := DecodePactos(data)
	if err != nil || len(ps) != 1 || ps[0].Status.EvaluatedRevision.Workload.UID != "u" || ps[0].Metadata.Generation != 3 {
		t.Fatalf("decode: %+v %v", ps, err)
	}
	if _, err := DecodePactos([]byte("{")); err == nil {
		t.Fatal("bad JSON must fail")
	}
	snap, err := Snapshot(context.Background(), ps, nil, []Revision{{Digest: dA}}, now)
	if err != nil || len(snap.Revisions) != 1 {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
	for _, r := range snap.Revisions {
		if r.Digest != c3 {
			t.Fatalf("revision identity must be the evaluated contract digest, got %q", r.Digest)
		}
	}
	if got := PactosFor(ps, "prod", "checkout"); len(got) != 1 {
		t.Fatal("PactosFor match")
	}
	if got := PactosFor(ps, "test", "checkout"); len(got) != 0 {
		t.Fatal("PactosFor namespace")
	}
	if parseTime("x") != nil || parseTime(now.Format(time.RFC3339)) == nil {
		t.Fatal("parseTime")
	}
}

func TestAdmitRollback(t *testing.T) {
	cited := &CitedOp{UID: "u1", Type: TypePromote, Applied: true, Pointer: PointerChange{Before: dA, After: dB}}
	rb := OperationSpec{Type: TypeRollback, Params: Params{From: dB, To: dA, Undoes: "u1"}}
	ok := StageStatus{Pointer: dB}
	cases := map[string]struct {
		spec   OperationSpec
		status StageStatus
		cited  *CitedOp
		class  ActorClass
		want   string
	}{
		"bound":       {rb, ok, cited, ClassAgent, ""},
		"unresolved":  {rb, ok, nil, ClassAgent, "names no operation"},
		"not applied": {rb, ok, &CitedOp{Applied: false}, ClassAgent, "never moved"},
		"moved":       {rb, StageStatus{Pointer: dC}, cited, ClassAgent, "from must be"},
		"wrong to":    {OperationSpec{Params: Params{From: dB, To: dC}}, ok, cited, ClassAgent, "to must be"},
		"empty before": {OperationSpec{Params: Params{From: dB}}, ok,
			&CitedOp{Applied: true, Pointer: PointerChange{After: dB}}, ClassAgent, "empty Stage"},
		"failed":            {rb, StageStatus{Pointer: dB, FailedDigests: []string{dA}}, cited, ClassAgent, "FailedDigest"},
		"frozen agent":      {rb, StageStatus{Pointer: dB, Frozen: true}, cited, ClassAgent, "Frozen"},
		"frozen human":      {rb, StageStatus{Pointer: dB, Frozen: true}, cited, ClassHuman, ""},
		"frozen agent fail": {OperationSpec{Params: rb.Params, Cause: &Cause{Verdict: &Verdict{Result: Fail}}}, StageStatus{Pointer: dB, Frozen: true}, cited, ClassAgent, ""},
	}
	for name, c := range cases {
		got := AdmitRollback(c.spec, c.status, c.cited, c.class)
		if (c.want == "" && got != "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestSetsAutoHoldAndDigestMemories(t *testing.T) {
	rb := OperationSpec{Type: TypeRollback, Params: Params{From: dB, To: dA, Undoes: "u1"}}
	st := StageStatus{Verified: []string{dA}}
	if SetsAutoHold(rb, ClassController, st) || !SetsAutoHold(rb, ClassHuman, st) || !SetsAutoHold(rb, ClassAgent, st) ||
		!SetsAutoHold(promote(dB, dA), ClassHuman, st) || SetsAutoHold(promote(dA, dB), ClassHuman, st) ||
		SetsAutoHold(OperationSpec{Type: TypeUnfreeze}, ClassHuman, st) {
		t.Fatal("SetsAutoHold")
	}

	l := AddDigest([]string{dA, dB}, dA, 2)
	if strings.Join(l, ",") != dB+","+dA {
		t.Fatalf("AddDigest moves to newest: %v", l)
	}
	if l = AddDigest(l, dC, 2); strings.Join(l, ",") != dA+","+dC {
		t.Fatalf("AddDigest caps: %v", l)
	}
	if l = RemoveDigest(l, dA); strings.Join(l, ",") != dC {
		t.Fatalf("RemoveDigest: %v", l)
	}
	if VerifyWindow(StageSpec{}) != 5*time.Minute || VerifyWindow(StageSpec{Gates: Gates{Verify: VerifyGates{Window: "30s"}}}) != 30*time.Second {
		t.Fatal("VerifyWindow")
	}
	if RecheckInterval(StageSpec{}) != time.Minute || RecheckInterval(StageSpec{Gates: Gates{Verify: VerifyGates{Recheck: "10s"}}}) != 10*time.Second {
		t.Fatal("RecheckInterval")
	}
}

func TestNewestCandidate(t *testing.T) {
	up := &StageStatus{Verified: []string{dA, dB, dC}, Offered: []string{dA, dB, dC}, FailedDigests: []string{dC}}
	if got := NewestCandidate(StageStatus{Pointer: dA}, "", up); got != dB {
		t.Fatalf("upstream newest unfailed: %q", got)
	}
	// Verified upstream but not offered there (an accepted unknown, or only
	// signed): never a candidate here.
	if got := NewestCandidate(StageStatus{Pointer: dA}, "", &StageStatus{Verified: []string{dA, dB}, Published: []string{dB}, Offered: []string{dA}}); got != "" {
		t.Fatalf("an unpublished upstream digest was offered: %q", got)
	}
	if got := NewestCandidate(StageStatus{Pointer: dB}, "", up); got != "" {
		t.Fatalf("already running: %q", got)
	}
	if got := NewestCandidate(StageStatus{Pointer: dC, FailedDigests: []string{dB}}, "", up); got != dA {
		t.Fatalf("own failures excluded: %q", got)
	}
	if got := NewestCandidate(StageStatus{Published: []string{dB}}, "v2@"+dB, nil); got != dB {
		t.Fatalf("oci candidate: %q", got)
	}
	if got := NewestCandidate(StageStatus{}, "v2@"+dB, nil); got != "" {
		t.Fatalf("a candidate Flux never verified is not offered: %q", got)
	}
	if got := NewestCandidate(StageStatus{FailedDigests: []string{dB}}, "v2@"+dB, nil); got != "" {
		t.Fatalf("failed oci candidate: %q", got)
	}
	if got := NewestCandidate(StageStatus{Pointer: dC, Verified: []string{dB, dC}}, "v1@"+dB, nil); got != "" {
		t.Fatalf("a tag moved back to an old digest is not new: %q", got)
	}
}

// observed is a Pacto that evaluated contract c2 at generation 2 with the given
// kernel coverage and findings.
func observed(coverage string, findings ...FindingView) PactoView {
	p := pactoAt("p", c2, "Warning", 2)
	p.Status.EvaluatedRevision.KernelCoverage = coverage
	p.Status.Findings = findings
	return p
}

func kernelFinding(code, subject, collector string) FindingView {
	return FindingView{Code: code, Severity: "warning", Subject: subject, Message: subject + " seen",
		EvidenceRefs: []EvidenceRefView{{Collector: collector, DetectedAt: "2026-10-20T10:01:00Z"}}}
}

func TestRuntimeVerdict(t *testing.T) {
	ref := "oci://r/pactos/payments@" + c2
	check := func(applied bool, p ...PactoView) Verdict {
		return RuntimeVerdict(ContractCheck{ContractRef: ref, Applied: applied, Pactos: p, Workload: live("w1", 2, 2)})
	}
	undeclared := kernelFinding("DEPENDENCY_UNDECLARED", "dependency/fraud", "ebpf")
	cases := []struct {
		name string
		v    Verdict
		want Result
		msg  string
	}{
		{"observed and clean", check(true, observed("Complete")), Pass, "the node agent saw nothing the contract does not declare"},
		{"undeclared call", check(true, observed("Complete", undeclared)), Fail, "calls or listens contrary to its contract: DEPENDENCY_UNDECLARED dependency/fraud"},
		{"contradiction under partial coverage", check(true, observed("Partial", undeclared)), Fail, "dependency/fraud"},
		{"port not listening", check(true, observed("Complete", kernelFinding("INTERFACE_ABSENT", "interface/api", "k8s-observer+ebpf"))), Fail, "INTERFACE_ABSENT"},
		{"api-only absence is not a claim", check(true, observed("Complete", kernelFinding("INTERFACE_ABSENT", "interface/api", "k8s-observer"))), Pass, "saw nothing"},
		{"other findings are not claims", check(true, observed("Complete", kernelFinding("DEPENDENCY_VERSION_MISMATCH", "dependency/x", "ebpf"))), Pass, "saw nothing"},
		{"kernel evidence off", check(true, observed("")), Unknown, "not observing"},
		{"partial coverage", check(true, observed("Partial")), Unknown, "coverage is Partial"},
		{"stale result", check(true, func() PactoView {
			p := observed("Complete", undeclared)
			p.Status.EvaluatedRevision.ContractDigest = c1
			return p
		}()), Unknown, "last evaluated contract"},
		{"not applied", check(false, observed("Complete")), Unknown, "not confirmed applied"},
		{"no pacto", check(true), Unknown, "no Pacto"},
	}
	for _, c := range cases {
		if c.v.Result != c.want || !strings.Contains(c.v.Message, c.msg) || c.v.Gate != GateRuntime || c.v.Phase != "verify" {
			t.Errorf("%s: got %+v, want %s containing %q", c.name, c.v, c.want, c.msg)
		}
	}
	v := check(true, observed("Complete", undeclared))
	if v.Citation["p.findings"] != "DEPENDENCY_UNDECLARED dependency/fraud (ebpf at 2026-10-20T10:01:00Z)" || v.Citation["p.kernelCoverage"] != "Complete" ||
		v.Citation["contract"] != ref {
		t.Errorf("citation: %v", v.Citation)
	}
	if v := check(true, observed("Complete")); v.Citation["p.kernelCoverage"] != "Complete" || v.Citation["p.evaluatedContract"] != c2 {
		t.Errorf("a Pass cites what was observed: %v", v.Citation)
	}
	if v := RuntimeVerdict(ContractCheck{ContractRef: "oci://r/p:1", Applied: true}); v.Result != Unknown {
		t.Errorf("tag: %+v", v)
	}
}

func TestDecodeKustomizations(t *testing.T) {
	ks, err := DecodeKustomizations([]byte(`{"items":[{"metadata":{"namespace":"test","name":"payments"},"spec":{"targetNamespace":"apps","sourceRef":{"kind":"OCIRepository","name":"payments","namespace":"flux"}}}]}`))
	if err != nil || len(ks) != 1 || ks[0].Spec.TargetNamespace != "apps" || ks[0].Spec.SourceRef.Namespace != "flux" {
		t.Fatalf("%+v %v", ks, err)
	}
	if _, err := DecodeKustomizations([]byte("{")); err == nil {
		t.Fatal("want a decode error")
	}
}
