package delivery

import (
	"context"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
)

const (
	rowRun  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	rowNext = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func rowStage(level Level, st StageStatus) StageObject {
	st.Level = level
	return StageObject{Metadata: ObjectMeta{Namespace: "prod", Name: "payments"},
		Spec: StageSpec{Service: ServiceRef{Name: "payments", Domain: "acme.io"}, Environment: "production"}, Status: st}
}

func rowCaller(class ActorClass, groups ...string) Caller {
	return Caller{Identity: Identity{User: "alice", Groups: groups}, Class: class, Policy: ClassPolicy{TeamGroupPrefix: "acme:"}}
}

func offerOf(row StageRow, action string) *Offer {
	if i := slices.IndexFunc(row.Offers, func(o Offer) bool { return o.Action == action }); i >= 0 {
		return &row.Offers[i]
	}
	return nil
}

// TestRowOfMatchesEnvironmentRow: the fields the board's environmentRow set,
// read the same way.
func TestRowOfMatchesEnvironmentRow(t *testing.T) {
	st := rowStage(LevelAdvise, StageStatus{Pointer: rowRun, Verified: []string{rowRun}, Candidate: rowNext, Frozen: true, FrozenReason: "Errored",
		Conditions: []Condition{{Type: ConditionReady, Status: "True", Reason: "Guarded"}}})
	read := map[string]Described{rowRun: {Version: "1.0.0", Contract: "oci://r/p@sha256:c"}, rowNext: {Version: "1.1.0"}}
	row := RowOf(st, RowInput{Held: 2, Caller: rowCaller(ClassHuman), Read: read})
	want := StageRow{Namespace: "prod", Stage: "payments", Service: "payments", Domain: "acme.io", Environment: "production",
		Pointer: rowRun, Version: "1.0.0", Verified: true, Frozen: true, FrozenReason: "Errored", Held: 2, Candidate: rowNext,
		CandidateVersion: "1.1.0", Ready: "True: Guarded", Actions: []string{"promote", "unfreeze"}, Level: "advise", State: "frozen",
		ContractRef: "oci://r/p@sha256:c"}
	got := row
	got.Offers, got.Watched = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("row\n got %+v\nwant %+v", got, want)
	}
	failed := RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun, FailedDigests: []string{rowRun},
		Conditions: []Condition{{Type: ConditionReady, Status: "False", Reason: "Weird", Message: "why"}}}), RowInput{})
	if !failed.Failed || failed.Ready != "False: Weird" || failed.ReadyMessage != "why" || failed.ReadyFix != "" || failed.State != "failed" ||
		failed.Actions == nil || len(failed.Actions) != 0 {
		t.Fatalf("failed row %+v", failed)
	}
}

func TestRowOfReadyFix(t *testing.T) {
	for reason, want := range map[string]string{ReadyReasonApplierNotOCI: ReadyFix(ReadyReasonApplierNotOCI), "Unknown": ""} {
		row := RowOf(rowStage(LevelAdvise, StageStatus{Conditions: []Condition{{Type: ConditionReady, Status: "False", Reason: reason}}}), RowInput{})
		if row.ReadyFix != want {
			t.Errorf("%s: fix %q", reason, row.ReadyFix)
		}
	}
	if row := RowOf(rowStage(LevelAdvise, StageStatus{Conditions: []Condition{{Type: ConditionReady, Status: "Unknown", Reason: ReadyReasonApplierNotOCI}}}), RowInput{}); row.ReadyFix != "" {
		t.Errorf("only a False Ready has a fix: %q", row.ReadyFix)
	}
}

func TestRowOfLastCheck(t *testing.T) {
	st := rowStage(LevelAdvise, StageStatus{Pointer: rowRun, LastOp: "uid-1", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun, At: "2027-01-01T00:00:00Z",
		Verdicts: []Verdict{{Gate: GateDiff, Result: Pass}, {Gate: "slo", Result: Unknown}}}})
	row := RowOf(st, RowInput{})
	if c := row.LastCheck; c == nil || !c.Current || c.Result != Unknown || c.At != "2027-01-01T00:00:00Z" || len(c.Verdicts) != 2 {
		t.Fatalf("lastCheck %+v", c)
	}
	st.Status.LastCheck.Verdicts = nil
	if c := RowOf(st, RowInput{}).LastCheck; c.Verdicts == nil || c.Result != Pass {
		t.Fatalf("no verdicts is an empty list: %+v", c)
	}
	if RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun}), RowInput{}).LastCheck != nil {
		t.Fatal("no re-check, no lastCheck")
	}
}

func TestRowOfLastCheckStale(t *testing.T) {
	for name, st := range map[string]StageStatus{
		"pointer moved":   {Pointer: rowNext, LastOp: "uid-1", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun}},
		"shipped again":   {Pointer: rowRun, LastOp: "uid-2", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun}},
		"nothing running": {LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun}},
	} {
		if c := RowOf(rowStage(LevelAdvise, st), RowInput{}).LastCheck; c == nil || c.Current {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

func TestRowOfWatched(t *testing.T) {
	st := rowStage(LevelAdvise, StageStatus{Pointer: rowRun, LastOp: "uid-1", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun},
		Conditions: []Condition{{Type: ConditionWatched, Status: "False", Reason: "RecheckFailed", Message: "slo failed", LastTransitionTime: "2027-01-02T00:00:00Z"}}})
	if w := RowOf(st, RowInput{}).Watched; w == nil || *w != (Watch{Status: "False", Reason: "RecheckFailed", Message: "slo failed", Since: "2027-01-02T00:00:00Z"}) {
		t.Fatalf("watched %+v", w)
	}
}

func TestRowOfWatchedStaleAfterPromote(t *testing.T) {
	notRechecked := Watch{Status: "Unknown", Reason: "NotRechecked", Message: "the running version has not been re-checked since it shipped"}
	watched := []Condition{{Type: ConditionWatched, Status: "True", Reason: "Rechecked", LastTransitionTime: "2027-01-02T00:00:00Z"}}
	for name, st := range map[string]StageStatus{
		"promoted since": {Pointer: rowNext, LastOp: "uid-2", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun}, Conditions: watched},
		"no lastCheck":   {Pointer: rowRun, Conditions: watched},
		"no condition":   {Pointer: rowRun, LastOp: "uid-1", LastCheck: &Recheck{Operation: "uid-1", Digest: rowRun}},
	} {
		if w := RowOf(rowStage(LevelAdvise, st), RowInput{}).Watched; w == nil || *w != notRechecked {
			t.Errorf("%s: %+v", name, w)
		}
	}
	if w := RowOf(rowStage(LevelAdvise, StageStatus{Conditions: watched}), RowInput{}).Watched; w != nil {
		t.Errorf("nothing runs, nothing is watched: %+v", w)
	}
}

type offerCase struct {
	name      string
	level     Level
	status    StageStatus
	caller    Caller
	read      map[string]Described
	available bool
	reason    string
}

func checkOffer(t *testing.T, action string, withService bool, cases []offerCase) {
	t.Helper()
	for _, c := range cases {
		level := c.level
		if level == "" {
			level = LevelAdvise
		}
		row := RowOf(rowStage(level, c.status), RowInput{Caller: c.caller, Read: c.read, WithService: withService})
		o := offerOf(row, action)
		if o == nil {
			t.Errorf("%s: no %s offer in %+v", c.name, action, row.Offers)
			continue
		}
		if o.Available != c.available || o.Reason != c.reason {
			t.Errorf("%s: %s offer %+v, want available=%v %q", c.name, action, *o, c.available, c.reason)
		}
		if inActions := slices.Contains(row.Actions, action); action != "freeze" && inActions != o.Available {
			t.Errorf("%s: actions %v disagree with the offer", c.name, row.Actions)
		}
	}
}

func TestPromoteOffer(t *testing.T) {
	cand := StageStatus{Pointer: rowRun, Candidate: rowNext}
	ready := func(st StageStatus, reason string) StageStatus {
		st.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: reason}}
		return st
	}
	frozen := cand
	frozen.Frozen = true
	read := map[string]Described{rowNext: {Version: "1.1.0"}}
	checkOffer(t, "promote", false, []offerCase{
		{name: "person", status: cand, caller: rowCaller(ClassHuman), read: read, available: true, reason: "promotes 1.1.0"},
		{name: "agent, unread version", status: cand, caller: rowCaller(ClassAgent), available: true, reason: "promotes sha256:222222222222"},
		{name: "break-glass", status: cand, caller: rowCaller(ClassBreakGlass), available: false, reason: "a break-glass caller may not raise a promote"},
		{name: "controller", status: cand, caller: rowCaller(ClassController), read: read, available: true, reason: "promotes 1.1.0"},
		{name: "frozen", status: frozen, caller: rowCaller(ClassHuman), read: read, available: true,
			reason: "promotes 1.1.0; the Stage is frozen, so the promote waits until a person unfreezes it"},
		{name: "observe", level: LevelObserve, status: cand, caller: rowCaller(ClassHuman), available: false,
			reason: "the Stage observes: GitOps owns the applyRef; propose an autonomy change to advise first"},
		{name: "unguarded", status: ready(cand, ReadyReasonUnguarded), caller: rowCaller(ClassHuman), available: false,
			reason: "the promoter admits no operation until the delivery admission policies are installed: " + ReadyFix(ReadyReasonUnguarded)},
		{name: "applier not automated", status: ready(cand, ReadyReasonApplierNotAutomated), caller: rowCaller(ClassHuman), read: read, available: true,
			reason: "promotes 1.1.0; the Stage is not ready (ApplierNotAutomated): " + ReadyFix(ReadyReasonApplierNotAutomated)},
		{name: "runs newest", status: StageStatus{Pointer: rowRun, Candidate: rowRun}, caller: rowCaller(ClassHuman), available: false,
			reason: "the Stage runs its newest candidate"},
		{name: "no candidate", status: StageStatus{Pointer: rowRun}, caller: rowCaller(ClassHuman), available: false,
			reason: "no eligible candidate: the candidate source offers nothing newer that has not failed here"},
		{name: "no candidate, source broken", status: ready(StageStatus{}, ReadyReasonCandidateNotFound), caller: rowCaller(ClassHuman), available: false,
			reason: "no eligible candidate: " + ReadyFix(ReadyReasonCandidateNotFound)},
	})
}

func TestUnfreezeOffer(t *testing.T) {
	frozen := StageStatus{Pointer: rowRun, Frozen: true}
	owned := map[string]Described{rowRun: {Team: "team-payments"}}
	strict := func(c Caller) Caller { c.Policy.RequireOwnerTeam = true; return c }
	checkOffer(t, "unfreeze", false, []offerCase{
		{name: "owner team", status: frozen, caller: rowCaller(ClassHuman, "acme:team-payments"), read: owned, available: true, reason: "unfreezes the Stage"},
		{name: "auto-hold", status: StageStatus{Pointer: rowRun, AutoHold: true}, caller: rowCaller(ClassHuman), available: true, reason: "unfreezes the Stage"},
		{name: "other team", status: frozen, caller: rowCaller(ClassHuman, "acme:team-ledger"), read: owned, available: false,
			reason: "only a person of team team-payments may unfreeze it; that team or break-glass can"},
		{name: "no team, strict", status: frozen, caller: strict(rowCaller(ClassHuman)), available: false,
			reason: "the running contract declares no team or cannot be read, and only an owner team may unfreeze it; break-glass can"},
		{name: "unreadable, strict", status: frozen, caller: strict(rowCaller(ClassHuman)), read: map[string]Described{rowRun: {Err: "registry down"}}, available: false,
			reason: "the running contract declares no team or cannot be read (registry down), and only an owner team may unfreeze it; break-glass can"},
		{name: "break-glass", status: frozen, caller: rowCaller(ClassBreakGlass), read: owned, available: true, reason: "unfreezes the Stage"},
		{name: "agent", status: frozen, caller: rowCaller(ClassAgent), available: false, reason: "an agent may not raise an unfreeze"},
		{name: "controller", status: frozen, caller: rowCaller(ClassController), available: false, reason: "a controller may not raise an unfreeze"},
	})
	if o := offerOf(RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun}), RowInput{Caller: rowCaller(ClassHuman)}), "unfreeze"); o != nil {
		t.Errorf("a running Stage offers no unfreeze: %+v", o)
	}
}

func TestFreezeOffer(t *testing.T) {
	running := StageStatus{Pointer: rowRun}
	read := map[string]Described{rowRun: {Version: "1.0.0"}}
	checkOffer(t, "freeze", true, []offerCase{
		{name: "person", status: running, caller: rowCaller(ClassHuman), read: read, available: true, reason: "freezes the Stage on 1.0.0"},
		{name: "unread version", status: running, caller: rowCaller(ClassBreakGlass), available: true, reason: "freezes the Stage on sha256:111111111111"},
		{name: "agent", status: running, caller: rowCaller(ClassAgent), available: false, reason: "an agent may not raise a freeze"},
		{name: "controller", status: running, caller: rowCaller(ClassController), available: true, reason: "freezes the Stage on sha256:111111111111"},
	})
	for name, st := range map[string]StageStatus{"frozen": {Pointer: rowRun, Frozen: true}, "empty": {}} {
		if o := offerOf(RowOf(rowStage(LevelAdvise, st), RowInput{Caller: rowCaller(ClassHuman), WithService: true}), "freeze"); o != nil {
			t.Errorf("%s: no freeze offer: %+v", name, o)
		}
	}
}

func TestFreezeOfferOnlyWithService(t *testing.T) {
	row := RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun}), RowInput{Caller: rowCaller(ClassHuman)})
	if o := offerOf(row, "freeze"); o != nil || slices.Contains(row.Actions, "freeze") {
		t.Fatalf("the board offers no freeze: %+v", row.Offers)
	}
}

func TestRowOfPolicyUnreadable(t *testing.T) {
	c := rowCaller(ClassBreakGlass)
	c.PolicyErr = `configmaps "pacto-delivery-identities" not found; the ConfigMap pacto-delivery-identities is not in x: pass --delivery-namespace with the namespace the chart's delivery.namespace names`
	row := RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun, Candidate: rowNext, AutoHold: true}), RowInput{Caller: c, WithService: true})
	want := "Pacto could not read the delivery identity policy (" + c.PolicyErr + "), so it cannot tell what you may ask for; admission decides when you propose"
	if len(row.Offers) != 3 || len(row.Actions) != 0 {
		t.Fatalf("row %+v", row)
	}
	for _, o := range row.Offers {
		if o.Available || o.Reason != want {
			t.Errorf("offer %+v", o)
		}
	}
}

// TestOffersNeverWrite: RowOf is handed no client, callback or channel, so it
// cannot change anything; offers only describe.
func TestOffersNeverWrite(t *testing.T) {
	var check func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	check = func(ty reflect.Type, path string) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		switch ty.Kind() {
		case reflect.Func, reflect.Interface, reflect.Chan, reflect.UnsafePointer:
			t.Errorf("%s is a %s: RowOf could call out through it", path, ty.Kind())
		case reflect.Pointer, reflect.Slice, reflect.Array:
			check(ty.Elem(), path+"[]")
		case reflect.Map:
			check(ty.Key(), path+"{key}")
			check(ty.Elem(), path+"{}")
		case reflect.Struct:
			for i := range ty.NumField() {
				check(ty.Field(i).Type, path+"."+ty.Field(i).Name)
			}
		}
	}
	fn := reflect.TypeOf(RowOf)
	for i := range fn.NumIn() {
		check(fn.In(i), fn.In(i).Name())
	}
}

func TestStageStateMatchesBoard(t *testing.T) {
	for _, c := range []struct {
		st          StageStatus
		held        int
		heldUnknown bool
		want        string
	}{
		{StageStatus{Pointer: rowRun, FailedDigests: []string{rowRun}, Frozen: true}, 1, true, "failed"},
		{StageStatus{Pointer: rowRun, Frozen: true, AutoHold: true}, 1, true, "frozen"},
		{StageStatus{Pointer: rowRun, AutoHold: true}, 1, true, "autohold"},
		{StageStatus{Pointer: rowRun, Verified: []string{rowRun}}, 0, true, "unknown"},
		{StageStatus{}, 0, true, "unknown"},
		{StageStatus{Pointer: rowRun}, 2, false, "waiting"},
		{StageStatus{}, 0, false, "empty"},
		{StageStatus{Pointer: rowRun}, 0, false, "unverified"},
		{StageStatus{Pointer: rowRun, Verified: []string{rowRun}}, 0, false, "verified"},
	} {
		if got := StageState(rowStage(LevelAdvise, c.st), c.held, c.heldUnknown); got != c.want {
			t.Errorf("%+v held=%d unknown=%v: %s, want %s", c.st, c.held, c.heldUnknown, got, c.want)
		}
	}
	if row := RowOf(rowStage(LevelAdvise, StageStatus{Pointer: rowRun}), RowInput{HeldUnknown: true}); row.State != "unknown" || !row.HeldUnknown {
		t.Errorf("row %+v", row)
	}
}

func TestClassPhrase(t *testing.T) {
	for c, want := range map[ActorClass]string{ClassHuman: "a person", ClassAgent: "an agent", ClassBreakGlass: "a break-glass caller",
		ClassController: "a controller", "robot": "a robot caller"} {
		if got := classPhrase(c); got != want {
			t.Errorf("%s: %q", c, got)
		}
	}
}

func TestDescribeAll(t *testing.T) {
	st := rowStage(LevelAdvise, StageStatus{})
	var inFlight, most atomic.Int32
	digests := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	out := DescribeAll(context.Background(), len(digests), func(i int) (*StageObject, string) { return &st, digests[i] },
		func(_ context.Context, s *StageObject, d string) Revision {
			n := inFlight.Add(1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			defer inFlight.Add(-1)
			return Revision{Digest: s.Metadata.Name + "@" + d}
		})
	for i, r := range out {
		if r.Digest != "payments@"+digests[i] {
			t.Errorf("out[%d] = %s", i, r.Digest)
		}
	}
	if most.Load() > describeParallel {
		t.Errorf("%d at once", most.Load())
	}
}
