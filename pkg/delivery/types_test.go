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
	if len(ts) != 8 {
		t.Fatalf("want the four Tier 0 types, freeze, autonomy, acknowledge and trust, got %d", len(ts))
	}
	ts[0].ID = "mutated"
	if p, _ := TypeByID(TypePromote); p.ID != TypePromote {
		t.Fatal("Types must return a copy")
	}
	if _, ok := TypeByID("delivery.pacto.trianalab.io/scale"); ok {
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
		{TypeAcknowledge, ClassHuman, false, true},
		{TypeAcknowledge, ClassBreakGlass, false, true},
		{TypeAcknowledge, ClassAgent, false, false},
		{TypeAcknowledge, ClassController, false, false},
		{TypeFreeze, ClassAgent, false, false},
		{TypeFreeze, ClassHuman, false, true},
		{TypeFreeze, ClassController, false, true},
		{"delivery.pacto.trianalab.io/scale", ClassHuman, false, false},
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

// TestOwnerTeamAndHumanGroups: a required owner team leaves an ownerless
// question to nobody, and a human allowlist makes everyone outside it an agent.
func TestOwnerTeamAndHumanGroups(t *testing.T) {
	if (ClassPolicy{RequireOwnerTeam: true}).InTeams([]string{"devs"}, nil) {
		t.Error("with an owner team required, a question naming no team has nobody to answer it")
	}
	pol := ClassPolicy{HumanGroups: []string{"staff"}, BreakGlassGroups: []string{"pacto:break-glass"}}
	cases := map[string]struct {
		id    Identity
		class ActorClass
		human bool
	}{
		"member":          {Identity{User: "alice", Groups: []string{"staff"}}, ClassHuman, true},
		"not enrolled":    {Identity{User: "bot", Groups: []string{"devs"}}, ClassAgent, false},
		"no groups":       {Identity{User: "bot"}, ClassAgent, false},
		"break-glass":     {Identity{User: "ops", Groups: []string{"pacto:break-glass"}}, ClassBreakGlass, false},
		"service account": {Identity{User: "system:serviceaccount:a:b", Groups: []string{"staff"}}, ClassAgent, false},
	}
	for name, c := range cases {
		if got := pol.Classify(c.id); got != c.class {
			t.Errorf("%s: Classify = %s, want %s", name, got, c.class)
		}
		if got := pol.IsHuman(c.id); got != c.human {
			t.Errorf("%s: IsHuman = %v, want %v", name, got, c.human)
		}
	}
}

// TestAdmitAcknowledge: an acknowledge needs a Stage that follows records
// from another cluster, a record id and a reason; other types pass through.
func TestAdmitAcknowledge(t *testing.T) {
	follows := StageSpec{Candidates: Candidates{UpstreamRecords: &RemoteStage{Stream: "c2/prod/payments", Repository: "reg/streams"}}}
	rec := "sha256:" + strings.Repeat("a", 64)
	for want, c := range map[string]struct {
		spec  OperationSpec
		stage StageSpec
	}{
		"":                  {OperationSpec{Type: TypeAcknowledge, Reason: "checked", Params: Params{Record: rec}}, follows},
		" ":                 {OperationSpec{Type: TypeUnfreeze}, StageSpec{}},
		"NoUpstreamRecords": {OperationSpec{Type: TypeAcknowledge, Reason: "checked", Params: Params{Record: rec}}, StageSpec{}},
		"InvalidRecord":     {OperationSpec{Type: TypeAcknowledge, Reason: "checked", Params: Params{Record: "seq 2"}}, follows},
		"ReasonRequired":    {OperationSpec{Type: TypeAcknowledge, Reason: "  ", Params: Params{Record: rec}}, follows},
	} {
		got := AdmitAcknowledge(c.spec, c.stage)
		if strings.TrimSpace(want) == "" && got != "" || !strings.HasPrefix(got, strings.TrimSpace(want)) {
			t.Errorf("%q: %q", want, got)
		}
	}
	p := PlanFor(context.Background(), PlanInput{Spec: OperationSpec{Type: TypeAcknowledge, Facet: FacetUpstream, Params: Params{Record: rec}}, Stage: follows, Class: ClassHuman})
	if p.Predicted != PredictRejected || !strings.HasPrefix(p.Conflicts[0], "ReasonRequired") {
		t.Fatalf("the plan predicts the refusal: %+v", p)
	}
	p = PlanFor(context.Background(), PlanInput{Spec: OperationSpec{Type: TypeAcknowledge, Facet: FacetUpstream, Reason: "checked", Params: Params{Record: rec}}, Stage: follows, Class: ClassHuman})
	if p.Predicted != PredictProceeds || p.Floor.Human {
		t.Fatalf("an acknowledge needs nobody else: %+v", p)
	}
}

func TestPlanGatesKeepsOnlyEvalProviders(t *testing.T) {
	g := ClassPolicy{GateProviders: map[string]string{"evals": ProviderEval, "mira": ProviderMira}}.PlanGates()
	if _, eval := g["evals"].(Eval); !eval || len(g) != 1 {
		t.Fatalf("plan gates %v", g)
	}
}

func TestInTeams(t *testing.T) {
	pol := ClassPolicy{TeamGroupPrefix: "acme:", TeamNarrowing: true}
	cases := []struct {
		groups, teams []string
		want          bool
	}{
		{nil, nil, true}, // no team named: any person
		{[]string{"team-checkout"}, []string{"team-checkout"}, false}, // with a prefix only the prefixed group counts
		{[]string{"acme:team-checkout"}, []string{"team-ledger", "team-checkout"}, true},
		{[]string{"team-payments", "acme:team-payments"}, []string{"team-checkout"}, false},
		{nil, []string{"team-checkout"}, false},
	}
	for _, c := range cases {
		if got := pol.InTeams(c.groups, c.teams); got != c.want {
			t.Errorf("InTeams(%v, %v) = %v", c.groups, c.teams, got)
		}
	}
	if (ClassPolicy{TeamNarrowing: true}).InTeams([]string{"team-checkout"}, []string{"checkout"}) {
		t.Error("with no prefix only the exact name counts")
	}
	if !(ClassPolicy{TeamNarrowing: true}).InTeams([]string{"team-checkout"}, []string{"team-checkout"}) {
		t.Error("with no prefix the exact name counts")
	}
	if (ClassPolicy{TeamNarrowing: true}).InTeams([]string{"system:authenticated"}, []string{"system:authenticated"}) {
		t.Error("a system: group every user carries is never a team")
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
	as := ApprovalStatus{Record: &ApprovalRecordRef{Digest: "sha256:ab", Ref: "r@sha256:cd"}}
	var as2 ApprovalStatus
	as.DeepCopyInto(&as2)
	if as2.Record == as.Record {
		t.Fatal("the approval status copy aliases the original")
	}
	if !reflect.DeepEqual(spec, spec2) || !reflect.DeepEqual(op, op2) || !reflect.DeepEqual(os, os2) || !reflect.DeepEqual(ap, ap2) || !reflect.DeepEqual(as, as2) {
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

// hangingRegistry answers nothing until its caller gives up, like a registry
// whose host drops packets.
type hangingRegistry struct{ hangPull bool }

func (h hangingRegistry) Annotations(ctx context.Context, _ string) (map[string]string, error) {
	if h.hangPull {
		return map[string]string{ContractAnnotation: "oci://r/p@" + c1}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (hangingRegistry) Pull(ctx context.Context, _ string) (*contract.Bundle, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestResolveRevisionGivesUpOnARegistryThatNeverAnswers(t *testing.T) {
	defer func(d time.Duration) { registryTimeout = d }(registryTimeout)
	registryTimeout = 20 * time.Millisecond
	for _, h := range []hangingRegistry{{}, {hangPull: true}} {
		r := ResolveRevision(context.Background(), h, "r/a", dA)
		if r.Bundle != nil || r.Err != "the registry did not answer within 20ms" {
			t.Fatalf("hung lookup: %+v", r)
		}
	}
	// A caller that gives up first says so in its own words.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := ResolveRevision(ctx, hangingRegistry{}, "r/a", dA); r.Err != context.Canceled.Error() {
		t.Fatalf("cancelled lookup: %+v", r)
	}
	ctx, done := WithRegistryTimeout(context.Background())
	defer done()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > registryTimeout {
		t.Fatalf("WithRegistryTimeout sets no bound: %v %v", dl, ok)
	}
}

func TestParseClassPolicy(t *testing.T) {
	p, err := ParseClassPolicy([]byte(`{"agentGroups":["bots"],"breakGlassGroups":["sre"],"teamGroupPrefix":"acme:","requireOwnerTeam":true,"humanGroups":["staff"]}`))
	if err != nil || p.TeamGroupPrefix != "acme:" || !p.RequireOwnerTeam || p.HumanGroups[0] != "staff" || p.AgentGroups[0] != "bots" || p.BreakGlassGroups[0] != "sre" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ParseClassPolicy([]byte("{")); err == nil {
		t.Fatal("a broken policy is an error")
	}
	if d := DefaultClassPolicy(); d.RequireOwnerTeam || d.AgentGroups[0] != "pacto:agents" || d.BreakGlassGroups[0] != "pacto:break-glass" {
		t.Fatalf("defaults: %+v", d)
	}
}

// TestParseClassPolicyOlderPublisherNarrows: a chart from before narrowing
// was opt-in always published teamGroupPrefix and never teamNarrowing, and
// its promoter always narrows; the chart now writes neither or both.
func TestParseClassPolicyOlderPublisherNarrows(t *testing.T) {
	for in, want := range map[string]bool{
		`{"teamGroupPrefix":"","requireOwnerTeam":true}`:                         true,
		`{"teamGroupPrefix":"acme:","teamNarrowing":true}`:                       true,
		`{"teamGroupPrefix":"","teamNarrowing":false}`:                           false,
		`{"agentGroups":["pacto:agents"],"requireOwnerTeam":false}`:              false,
		`{"breakGlassGroups":["sre"],"teamNarrowing":true,"teamGroupPrefix":""}`: true,
	} {
		if p, err := ParseClassPolicy([]byte(in)); err != nil || p.TeamNarrowing != want {
			t.Errorf("%s: narrowing %v, want %v (%v)", in, p.TeamNarrowing, want, err)
		}
	}
}

// TestClassifyBySources: every class says where it came from, and nobody
// who signs in as a system: user or unauthenticated is a person.
func TestClassifyBySources(t *testing.T) {
	pol := ClassPolicy{ControllerUser: "system:serviceaccount:pacto:promoter", AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}
	staff := pol
	staff.HumanGroups = []string{"staff"}
	for _, c := range []struct {
		name   string
		pol    ClassPolicy
		id     Identity
		class  ActorClass
		source ClassSource
	}{
		{"promoter", pol, Identity{User: pol.ControllerUser}, ClassController, ClassByController},
		{"break-glass", pol, Identity{User: "ops", Groups: []string{"pacto:break-glass"}}, ClassBreakGlass, ClassByBreakGlassGroup},
		{"service account", pol, Identity{User: "system:serviceaccount:ci:ci-bot"}, ClassAgent, ClassByServiceAccount},
		{"oidc agent", pol, Identity{User: "https://idp#bot", Groups: []string{"pacto:agents"}}, ClassAgent, ClassByAgentGroup},
		{"outside human groups", staff, Identity{User: "dana@example.com", Groups: []string{"devs"}}, ClassAgent, ClassByOutsideHumanGroups},
		{"oidc person", pol, Identity{User: "dana@example.com", Groups: []string{"devs"}}, ClassHuman, ClassByUser},
		{"x509 admin", pol, Identity{User: "kubernetes-admin", Groups: []string{"kubeadm:cluster-admins", "system:authenticated"}}, ClassHuman, ClassByUser},
		{"scheduler", pol, Identity{User: "system:kube-scheduler"}, ClassAgent, ClassBySystemUser},
		{"node", pol, Identity{User: "system:node:kind-worker", Groups: []string{"system:nodes"}}, ClassAgent, ClassBySystemUser},
		{"anonymous", pol, Identity{User: "system:anonymous"}, ClassAgent, ClassBySystemUser},
		{"unauthenticated", pol, Identity{User: "alice", Groups: []string{"system:unauthenticated"}}, ClassAgent, ClassBySystemUser},
	} {
		class, source := c.pol.ClassifyBy(c.id)
		if class != c.class || source != c.source {
			t.Errorf("%s: ClassifyBy = %s/%s, want %s/%s", c.name, class, source, c.class, c.source)
		}
		if got := c.pol.Classify(c.id); got != class {
			t.Errorf("%s: Classify = %s, ClassifyBy says %s", c.name, got, class)
		}
		if c.source == ClassBySystemUser && c.pol.IsHuman(c.id) {
			t.Errorf("%s: a system user is a person", c.name)
		}
	}
}

// TestInTeamsNarrowing: owner teams narrow who may answer only when the
// policy says so.
func TestInTeamsNarrowing(t *testing.T) {
	off := ClassPolicy{TeamGroupPrefix: "acme:"}
	if !off.InTeams([]string{"devs"}, []string{"checkout"}) || !off.InTeams(nil, []string{"checkout"}) {
		t.Error("with narrowing off any groups answer a named team")
	}
	if !off.InTeams(nil, nil) || (ClassPolicy{RequireOwnerTeam: true}).InTeams([]string{"devs"}, nil) {
		t.Error("with no team named the answer is !RequireOwnerTeam")
	}
	on := ClassPolicy{TeamGroupPrefix: "acme:", TeamNarrowing: true}
	if !on.InTeams([]string{"acme:checkout"}, []string{"checkout"}) || on.InTeams([]string{"checkout"}, []string{"checkout"}) {
		t.Error("with a prefix only the prefixed group counts")
	}
	exact := ClassPolicy{TeamNarrowing: true}
	if !exact.InTeams([]string{"checkout"}, []string{"checkout"}) || exact.InTeams([]string{"devs"}, []string{"checkout"}) {
		t.Error("with no prefix only the exact name counts")
	}
	if exact.InTeams([]string{"system:authenticated"}, []string{"system:authenticated"}) {
		t.Error("a system: group is never a team")
	}
}

func TestDefaultPolicyRequiresNoTeam(t *testing.T) {
	if d := DefaultClassPolicy(); d.RequireOwnerTeam || d.TeamNarrowing {
		t.Fatalf("defaults narrow or require a team: %+v", d)
	}
}
