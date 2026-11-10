package delivery

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
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
	for _, f := range []any{RowOf, UndoOffer, UndoOfferFor} {
		fn := reflect.TypeOf(f)
		for i := range fn.NumIn() {
			if fn.In(i) == reflect.TypeFor[error]() {
				continue // UndoOfferFor only prints it
			}
			check(fn.In(i), fn.In(i).Name())
		}
	}
	// NarrowOffers calls out only through can, its one func argument.
	fn := reflect.TypeOf(NarrowOffers)
	for i := range fn.NumIn() {
		if in := fn.In(i); in != reflect.TypeFor[context.Context]() && in != reflect.TypeFor[CanCreate]() {
			check(in, in.Name())
		}
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

const rowOld = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func undoEntry(typ, before, after, uid string) HistoryEntry {
	e := HistoryEntry{Operation: "ship-1", Type: typ, OperationUID: uid}
	if after != "" {
		e.Pointer = &PointerChange{Before: before, After: after}
	}
	return e
}

// TestUndoOffer: the rules of UndoOf and the rollback-only admission rules,
// applied only to the change the Stage runs; the offer never says rollback.
func TestUndoOffer(t *testing.T) {
	running := StageStatus{Pointer: rowRun, LastOp: "uid-1"}
	gone := "; if its Operation is gone from the cluster, undo is a promote back: open undo for the plan"
	for _, c := range []struct {
		name   string
		status StageStatus
		e      HistoryEntry
		caller Caller
		ok     bool
		reason string
	}{
		{"policy unreadable", running, undoEntry(TypePromote, rowOld, rowRun, "uid-1"), Caller{Class: ClassHuman, PolicyErr: "boom"}, false,
			"Pacto could not read the delivery identity policy (boom), so it cannot tell what you may ask for; admission decides when you propose"},
		{"moved nothing", running, undoEntry(TypeFreeze, "", "", "uid-1"), rowCaller(ClassHuman), false, "it moved nothing, so there is nothing to undo"},
		{"runs: person", running, undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller(ClassHuman), true, "undoes ship-1: back to " + short(rowOld)},
		{"runs: no uid falls back to the pointer", running, undoEntry(TypePromote, rowOld, rowRun, ""), rowCaller(ClassHuman), true,
			"undoes ship-1: back to " + short(rowOld)},
		{"runs: an unknown class may not roll back", running, undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller("robot"), false,
			"a robot caller may not raise a rollback; break-glass moves the pointer with an override: pacto delivery propose --type override" + gone},
		{"runs: agent undoes a rollback", running, undoEntry(TypeRollback, rowOld, rowRun, "uid-1"), rowCaller(ClassAgent), false,
			"only a person or break-glass undoes a rollback or an override; propose a promote to " + short(rowOld) + " instead" + gone},
		{"runs: first version", running, undoEntry(TypePromote, "", rowRun, "uid-1"), rowCaller(ClassHuman), false,
			"it brought the Stage its first version; there is no earlier version to go back to"},
		{"runs: replaced a failed version", StageStatus{Pointer: rowRun, LastOp: "uid-1", FailedDigests: []string{rowOld}},
			undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller(ClassHuman), false,
			"the version it replaced failed here, and Pacto never goes back to a failed version"},
		{"runs: agent on a frozen Stage", StageStatus{Pointer: rowRun, LastOp: "uid-1", Frozen: true},
			undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller(ClassAgent), true,
			"undoes ship-1: back to " + short(rowOld) + "; an agent rollback on a frozen Stage needs a Fail verdict as its cause"},
		{"A to B to A: another operation runs the same digest", StageStatus{Pointer: rowRun, LastOp: "uid-9"},
			undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller(ClassHuman), true,
			"the Stage changed since; going back to " + short(rowOld) + " is a promote, and every gate runs"},
		{"moved on: agent and an old rollback", StageStatus{Pointer: rowNext, LastOp: "uid-9"},
			undoEntry(TypeRollback, rowOld, rowRun, "uid-1"), rowCaller(ClassAgent), true,
			"the Stage changed since; going back to " + short(rowOld) + " is a promote, and every gate runs"},
		{"moved on: first version", StageStatus{Pointer: rowNext}, undoEntry(TypePromote, "", rowRun, "uid-1"), rowCaller(ClassHuman), false,
			"it brought the Stage its first version; there is no earlier version to go back to"},
		{"moved on: already back", StageStatus{Pointer: rowOld}, undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller(ClassHuman), false,
			"the Stage already runs " + short(rowOld) + ", the version it replaced"},
		{"moved on: failed", StageStatus{Pointer: rowNext, FailedDigests: []string{rowOld}}, undoEntry(TypePromote, rowOld, rowRun, "uid-1"),
			rowCaller(ClassHuman), false, "the version it replaced failed here, and Pacto never goes back to a failed version"},
		{"moved on: observe", StageStatus{Pointer: rowNext, Level: LevelObserve}, undoEntry(TypePromote, rowOld, rowRun, "uid-1"),
			rowCaller(ClassHuman), false, "the Stage observes: GitOps owns the applyRef, so going back is a promote it refuses; propose an autonomy change to advise first"},
		{"moved on: an unknown class", StageStatus{Pointer: rowNext}, undoEntry(TypePromote, rowOld, rowRun, "uid-1"), rowCaller("robot"), false,
			"a robot caller may not raise the promote back"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := UndoOffer(c.status, c.e, c.caller)
			if o.Action != "undo" || o.Available != c.ok || o.Reason != c.reason || o.Operation != "ship-1" {
				t.Fatalf("offer %+v", o)
			}
			if c.e.Pointer != nil && o.To != c.e.Pointer.Before {
				t.Fatalf("to %q", o.To)
			}
		})
	}
}

func TestUndoOfferForBlame(t *testing.T) {
	human, running := rowCaller(ClassHuman), StageStatus{Pointer: rowRun, LastOp: "uid-1"}
	if o := UndoOfferFor(running, nil, errors.New("forbidden"), human); o == nil || o.Available || o.Reason != "could not read its history: forbidden" {
		t.Fatalf("error %+v", o)
	}
	if o := UndoOfferFor(running, nil, nil, human); o != nil {
		t.Fatalf("nil blame %+v", o)
	}
	if o := UndoOfferFor(StageStatus{}, &Blame{}, nil, human); o == nil || o.Available || o.Reason != "the Stage runs nothing yet" {
		t.Fatalf("no pointer %+v", o)
	}
	if o := UndoOfferFor(running, &Blame{Note: "the history is partial"}, nil, human); o == nil || o.Available || o.Reason != "the history is partial" {
		t.Fatalf("no change %+v", o)
	}
	e := undoEntry(TypePromote, rowOld, rowRun, "uid-1")
	if o := UndoOfferFor(running, &Blame{Change: &e}, nil, human); o == nil || !o.Available || o.To != rowOld {
		t.Fatalf("change %+v", o)
	}
}

// TestNarrowOffersFailsClosed: a denied or failed review leaves nothing
// available, each namespace is asked once, and only for create operations.
func TestNarrowOffersFailsClosed(t *testing.T) {
	row := func(ns string, available bool) *StageRow {
		r := &StageRow{Namespace: ns, Actions: []string{}, Offers: []Offer{{Action: "freeze", Reason: "a person may not raise a freeze"}}}
		if available {
			r.Actions = []string{"promote"}
			r.Offers = append(r.Offers, Offer{Action: "promote", Available: true, Reason: "promotes 1.1.0"},
				Offer{Action: "undo", Available: true, Reason: "undoes ship-1"})
		}
		return r
	}
	rows := []*StageRow{row("ok", true), row("ok", true), row("denied", true), row("broken", true), row("quiet", false)}
	var mu sync.Mutex
	asked := map[string]int{}
	can := CreateOperations(func(_ context.Context, verb, resource, ns string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if verb != "create" || resource != "operations" {
			t.Errorf("asked %s %s", verb, resource)
		}
		asked[ns]++
		switch ns {
		case "ok":
			return true, nil
		case "broken":
			return true, errors.New("timeout")
		}
		return false, nil
	})
	NarrowOffers(context.Background(), rows, can)
	if !reflect.DeepEqual(asked, map[string]int{"ok": 1, "denied": 1, "broken": 1}) {
		t.Fatalf("asked %v", asked)
	}
	for _, r := range rows[:2] {
		if !slices.Equal(r.Actions, []string{"promote"}) || !r.Offers[1].Available || !r.Offers[2].Available {
			t.Fatalf("allowed row narrowed: %+v", r)
		}
	}
	expectNarrowed(t, rows[2], "your Kubernetes RBAC does not let you create Operations in denied")
	expectNarrowed(t, rows[3], "Pacto could not ask the apiserver whether you may create Operations in broken (timeout), so nothing is offered")
}

// expectNarrowed: r offers nothing, every offer that was available says
// why, and an offer that was not keeps its own reason.
func expectNarrowed(t *testing.T, r *StageRow, want string) {
	t.Helper()
	if len(r.Actions) != 0 || r.Actions == nil {
		t.Fatalf("actions %v", r.Actions)
	}
	for _, o := range r.Offers[1:] {
		if o.Available || o.Reason != want {
			t.Fatalf("offer %+v", o)
		}
	}
	if r.Offers[0].Reason != "a person may not raise a freeze" {
		t.Fatalf("an unavailable offer kept its own reason: %+v", r.Offers[0])
	}
}

// TestHistoryEntriesCarryOperationUID: UndoOffer tells a running change from
// another one that ran the same digest by the Operation's uid.
func TestHistoryEntriesCarryOperationUID(t *testing.T) {
	if e := StatusEntry(OperationObject{Metadata: ObjectMeta{Name: "ship", UID: "u1"}}); e.OperationUID != "u1" {
		t.Errorf("status entry %q", e.OperationUID)
	}
	var r StoredRecord
	r.Statement.Predicate.Intent.UID = "u2"
	if e := RecordEntry(r); e.OperationUID != "u2" {
		t.Errorf("record entry %q", e.OperationUID)
	}
}

// TestStageStateWord: every state reads as the dashboard's ENV_STATES word.
func TestStageStateWord(t *testing.T) {
	want := map[string]string{"failed": "failed", "frozen": "frozen", "autohold": "on auto-hold", "waiting": "waiting for a person",
		"empty": "running nothing yet", "unverified": "not verified", "verified": "verified", "unknown": "not known: its operations could not be read"}
	for state, word := range want {
		if got := StageStateWord(state); got != word {
			t.Errorf("%s: got %q, want %q", state, got, word)
		}
	}
}
