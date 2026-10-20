package delivery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestTier0Types(t *testing.T) {
	ts := Types()
	if len(ts) != 4 {
		t.Fatalf("want the four Tier 0 types, got %d", len(ts))
	}
	ts[0].ID = "mutated"
	if p, _ := TypeByID(TypePromote); p.ID != TypePromote {
		t.Fatal("Types must return a copy")
	}
	if _, ok := TypeByID("pacto.dev/scale"); ok {
		t.Fatal("unknown type resolved")
	}
	p, _ := TypeByID(TypePromote)
	if d := p.Digest(); !strings.HasPrefix(d, "sha256:") || d != p.Digest() {
		t.Fatalf("digest %q must be stable", d)
	}
	a, e := p.Deadlines(Deadlines{})
	if a != 24*time.Hour || e != 15*time.Minute {
		t.Fatalf("defaults: %v %v", a, e)
	}
	a, e = p.Deadlines(Deadlines{Approval: "1h", Execution: "nonsense"})
	if a != time.Hour || e != 15*time.Minute {
		t.Fatalf("overrides: %v %v", a, e)
	}
}

func TestMayRaise(t *testing.T) {
	cases := []struct {
		id        string
		class     ActorClass
		witnessed bool
		want      bool
	}{
		{TypePromote, ClassAgent, false, true},
		{TypePromote, ClassController, false, true},
		{TypeOverride, ClassAgent, false, false},
		{TypeOverride, ClassBreakGlass, false, true},
		{TypeOverride, ClassController, false, false},
		{TypeOverride, ClassController, true, true},
		{TypePromote, ClassController, true, false},
		{TypeOverride, ClassBreakGlass, true, false},
		{TypeUnfreeze, ClassAgent, false, false},
		{TypeUnfreeze, ClassHuman, false, true},
		{"pacto.dev/scale", ClassHuman, false, false},
	}
	for _, c := range cases {
		if got := MayRaise(c.id, c.class, c.witnessed); got != c.want {
			t.Errorf("MayRaise(%s, %s, %v) = %v", c.id, c.class, c.witnessed, got)
		}
	}
}

func TestClassify(t *testing.T) {
	pol := ClassPolicy{ControllerUser: "system:serviceaccount:pacto:promoter", AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}
	cases := map[ActorClass]Identity{
		ClassController: {User: "system:serviceaccount:pacto:promoter"},
		ClassBreakGlass: {User: "ops", Groups: []string{"pacto:break-glass"}},
		ClassAgent:      {User: "system:serviceaccount:agents:bot"},
		ClassHuman:      {User: "alice", Groups: []string{"devs"}},
	}
	for want, id := range cases {
		if got := pol.Classify(id); got != want {
			t.Errorf("Classify(%+v) = %s, want %s", id, got, want)
		}
	}
	if pol.Classify(Identity{User: "carol", Groups: []string{"pacto:agents"}}) != ClassAgent {
		t.Error("an agent group makes an agent")
	}
	if !pol.IsHuman(Identity{User: "alice"}) || pol.IsHuman(Identity{User: "system:serviceaccount:x:y"}) ||
		pol.IsHuman(Identity{User: "bob", Groups: []string{"pacto:agents"}}) || pol.IsHuman(Identity{User: pol.ControllerUser}) {
		t.Error("IsHuman")
	}
}

func TestInTeams(t *testing.T) {
	pol := ClassPolicy{TeamGroupPrefix: "acme:"}
	cases := []struct {
		groups, teams []string
		want          bool
	}{
		{nil, nil, true}, // no team named: any person
		{[]string{"team-checkout"}, []string{"team-checkout"}, true},
		{[]string{"acme:team-checkout"}, []string{"team-ledger", "team-checkout"}, true},
		{[]string{"team-payments", "acme:team-payments"}, []string{"team-checkout"}, false},
		{nil, []string{"team-checkout"}, false},
	}
	for _, c := range cases {
		if got := pol.InTeams(c.groups, c.teams); got != c.want {
			t.Errorf("InTeams(%v, %v) = %v", c.groups, c.teams, got)
		}
	}
	if (ClassPolicy{}).InTeams([]string{"team-checkout"}, []string{"checkout"}) {
		t.Error("with no prefix only the exact name counts")
	}
}

func TestDeepCopyIsIndependent(t *testing.T) {
	st := StageStatus{Locks: map[string]string{"pointer": "u1"}, Verified: []string{dA}}
	var out StageStatus
	st.DeepCopyInto(&out)
	out.Locks["pointer"], out.Verified[0] = "u2", dB
	if st.Locks["pointer"] != "u1" || st.Verified[0] != dA {
		t.Fatal("copy aliases the original")
	}
	spec := StageSpec{Service: ServiceRef{Name: "p"}, Candidates: Candidates{UpstreamStage: &StageRef{Name: "t"}}}
	var spec2 StageSpec
	spec.DeepCopyInto(&spec2)
	op := OperationSpec{Type: TypePromote, Actor: Actor{User: "u", Groups: []string{"g"}}, Cause: &Cause{Operation: "x"}}
	var op2 OperationSpec
	op.DeepCopyInto(&op2)
	os := OperationStatus{Plan: &Plan{Verdicts: []Verdict{{Gate: GateDiff}}}}
	var os2 OperationStatus
	os.DeepCopyInto(&os2)
	ap := ApprovalSpec{Operation: "o", Decision: Approve, Groups: []string{"team-a"}}
	var ap2 ApprovalSpec
	ap.DeepCopyInto(&ap2)
	if !reflect.DeepEqual(spec, spec2) || !reflect.DeepEqual(op, op2) || !reflect.DeepEqual(os, os2) || !reflect.DeepEqual(ap, ap2) {
		t.Fatal("copies differ")
	}
}

func TestDigests(t *testing.T) {
	if RevisionDigest("v1@"+dA) != dA || RevisionDigest(dA) != dA || RevisionDigest("v1") != "" || RevisionDigest("") != "" {
		t.Fatal("RevisionDigest")
	}
	if ContractDigest("oci://r/p@"+c1) != c1 || ContractDigest("oci://r/p:1.0.0") != "" {
		t.Fatal("ContractDigest")
	}
	if _, err := ParseDigest("latest"); err == nil {
		t.Fatal("a tag is not a digest")
	}
	if RepoFromURL("oci://r/p") != "r/p" {
		t.Fatal("RepoFromURL")
	}
}

type fakeRegistry struct {
	ann     map[string]string
	annErr  error
	pullErr error
	pulled  string
}

func (f *fakeRegistry) Annotations(context.Context, string) (map[string]string, error) {
	return f.ann, f.annErr
}

func (f *fakeRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	f.pulled = ref
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	return bundle("payments", "1.0.0"), nil
}

func TestResolveRevision(t *testing.T) {
	ctx := context.Background()
	if r := ResolveRevision(ctx, &fakeRegistry{}, "r/a", ""); r.Digest != "" || r.Err != "" {
		t.Fatalf("empty stage: %+v", r)
	}
	if r := ResolveRevision(ctx, &fakeRegistry{annErr: errors.New("down")}, "r/a", dA); r.Err != "down" {
		t.Fatalf("registry error: %+v", r)
	}
	if r := ResolveRevision(ctx, &fakeRegistry{ann: map[string]string{ContractAnnotation: "oci://r/p:1.0.0"}}, "r/a", dA); !strings.Contains(r.Err, "digest-pinned") {
		t.Fatalf("tag contract: %+v", r)
	}
	ref := "oci://r/p@" + c1
	if r := ResolveRevision(ctx, &fakeRegistry{ann: map[string]string{ContractAnnotation: ref}, pullErr: errors.New("nope")}, "r/a", dA); r.Err != "nope" || r.ContractRef != ref {
		t.Fatalf("pull error: %+v", r)
	}
	reg := &fakeRegistry{ann: map[string]string{ContractAnnotation: ref}}
	if r := ResolveRevision(ctx, reg, "r/a", dA); r.Bundle == nil || reg.pulled != "r/p@"+c1 {
		t.Fatalf("resolved: %+v pulled %q", r, reg.pulled)
	}
}

func TestParseAuthTime(t *testing.T) {
	if got, ok := ParseAuthTime("1760954400"); !ok || got.Unix() != 1760954400 {
		t.Fatal("unix seconds")
	}
	if got, ok := ParseAuthTime("2026-10-20T10:00:00Z"); !ok || !got.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("RFC 3339")
	}
	if _, ok := ParseAuthTime("yesterday"); ok {
		t.Fatal("anything else is not a login time")
	}
}
