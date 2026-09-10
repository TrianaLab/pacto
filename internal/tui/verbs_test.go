package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestVerbKeysAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, v := range verbList(newLoadedContext(t)) {
		if prev, dup := seen[v.Key]; dup {
			t.Errorf("key %q is bound to both %q and %q", v.Key, prev, v.Help)
		}
		seen[v.Key] = v.Help
	}
	for _, g := range globalBindings() {
		if help, clash := seen[g.Key]; clash {
			t.Errorf("key %q is both a global (%q) and a verb (%q)", g.Key, g.Help, help)
		}
	}
}

func TestReadOnlyHidesEveryWriteVerb(t *testing.T) {
	c := newLoadedContext(t)
	c.ReadOnly = true
	for _, v := range verbList(c) {
		if v.Write {
			t.Errorf("write verb %q is offered in read-only mode", v.Key)
		}
	}
}

func TestEveryVerbHasHelpAndAnArgv(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindRevision, Key: "k", Label: "l", Ref: "./svc"}
	for _, v := range verbList(c) {
		if v.Help == "" || v.Run == nil || v.Argv == nil || v.Applies == nil {
			t.Errorf("verb %q is incomplete: %+v", v.Key, v)
			continue
		}
		if argv := v.Argv(c, sel); len(argv) == 0 || argv[0] != "pacto" {
			t.Errorf("verb %q produced argv %v; it must start with pacto so y can yank it", v.Key, argv)
		}
	}
}

// TestOnlyYankAppliesToAnOwner is an allow-list of one, deliberately. Narrowing
// it to the three verbs it used to name read as exhaustive and was not: d and i
// were free to be loosened to always, which arms a diff whose left-hand side is
// the empty string, and a new bundle verb was born untested.
//
// An owner is an aggregation over contracts, not a contract, so y -- which
// yanks the fleet search line for exactly this case -- is the only verb that has
// anything to offer. The reason has to name the owner for every verb whose
// predicate keys on the bundle; e and g decline on kind instead and say so in
// their own words, which is why they are exempt from the wording check and from
// nothing else.
func TestOnlyYankAppliesToAnOwner(t *testing.T) {
	c := newLoadedContext(t)
	owner := Selection{Kind: fleet.KindOwner, Key: "team:x", Label: "x"}
	for _, v := range verbList(c) {
		if v.Key == "y" {
			if why := v.Applies(owner); why != "" {
				t.Errorf("y declined an owner with %q; it is the one verb an owner row has", why)
			}
			continue
		}
		why := v.Applies(owner)
		if why == "" {
			t.Errorf("verb %q claims to apply to an owner, which has no bundle", v.Key)
			continue
		}
		if v.Key == "e" || v.Key == graphVerbKey {
			continue
		}
		if !strings.Contains(why, "owner") {
			t.Errorf("verb %q rejected an owner with %q, which does not say what is wrong", v.Key, why)
		}
	}
}

// TestGraphVerbRootsTheWayFleetGraphDoes pins the g verb's line. fleet graph
// takes at most one positional, so "graph <kind> <key>" fails on arity before it
// can look anything up; a revision and a target each go on their own flag, and
// an owner or a source is not a root the neighborhood resolver accepts at all.
func TestGraphVerbRootsTheWayFleetGraphDoes(t *testing.T) {
	c := newLoadedContext(t)
	g := verbBoundTo(t, c, "g")

	for _, tt := range []struct {
		name       string
		sel        Selection
		want       []string
		wantReject string
	}{
		{
			"a service is positional",
			Selection{Kind: fleet.KindService, Key: "svc"},
			[]string{"pacto", "fleet", "graph", "svc"}, "",
		},
		{
			"a revision goes on --revision",
			Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0"},
			[]string{"pacto", "fleet", "graph", "--revision", "svc@1.0.0"}, "",
		},
		{
			"a target goes on --target",
			Selection{Kind: fleet.KindTarget, Key: "prod/Deployment/svc"},
			[]string{"pacto", "fleet", "graph", "--target", "prod/Deployment/svc"}, "",
		},
		{
			"an owner is not a root",
			Selection{Kind: fleet.KindOwner, Key: "team:x"},
			nil, "roots at",
		},
		{
			"a source is not a root",
			Selection{Kind: fleet.KindSource, Key: "local"},
			nil, "roots at",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			why := g.Applies(tt.sel)
			if tt.wantReject != "" {
				if !strings.Contains(why, tt.wantReject) {
					t.Fatalf("g.Applies = %q, want it to mention %q", why, tt.wantReject)
				}
				return
			}
			if why != "" {
				t.Fatalf("g does not apply to %s: %s", tt.sel.Kind, why)
			}
			if got := g.Argv(c, tt.sel); !slices.Equal(got, tt.want) {
				t.Fatalf("g argv = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheGraphVerbCarriesEveryFieldASelectionReads closes the one seam where a
// selection is taken apart and put back together. g is the only verb that
// navigates: it rebuilds a fleet.EntityRef field by field from the Selection it
// was handed, and the screen it opens is itself a row verbs dispatch against. So
// any field resolveSelection reads and that rebuild forgets is a field the next
// verb sees as empty — which is how e came to explain the empty string on the
// graph of a revision. Round-tripping through the real screen fails the moment a
// field is added to one side and not the other.
func TestTheGraphVerbCarriesEveryFieldASelectionReads(t *testing.T) {
	c := newLoadedContext(t)
	sel, err := resolveSelection(c, firstEntityOfKind(t, c, fleet.KindRevision))
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}

	g, ok := verbBoundTo(t, c, "g").Run(c, sel)().(pushMsg).s.(*graphScreen)
	if !ok {
		t.Fatal("g did not push a graph screen")
	}
	ref, _ := g.selected()
	again, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatalf("the graph screen's own row does not resolve: %v", err)
	}
	if again != sel {
		t.Fatalf("a selection does not survive the graph screen:\n got %+v\nwant %+v\n"+
			"the g verb rebuilds fleet.EntityRef field by field in verbs.go; carry the new field there too",
			again, sel)
	}
}

// TestFleetExplainVerbNamesASubjectFleetExplainResolves pins e's subject.
// Query.Explain resolves a service key or name and then a target key
// (pkg/fleet/query.go:908-924) and nothing else, so handing it the row's own key
// on a revision, an owner or a source opened an error screen on three of the
// five kinds the list has a tab for. A revision explains the service it is a
// revision OF; an owner and a source decline the way g does.
func TestFleetExplainVerbNamesASubjectFleetExplainResolves(t *testing.T) {
	c := newLoadedContext(t)
	e := verbBoundTo(t, c, "e")

	for _, tt := range []struct {
		name       string
		sel        Selection
		want       []string
		wantReject string
	}{
		{
			"a service is its own subject",
			Selection{Kind: fleet.KindService, Key: "shop/checkout"},
			[]string{"pacto", "fleet", "explain", "shop/checkout"}, "",
		},
		{
			// The parent key, never the revision key with the @version cut off:
			// ParentService is domain-qualified, and a bare "checkout" would name
			// a different service in a fleet that also holds ops/checkout.
			"a revision explains its parent service",
			Selection{Kind: fleet.KindRevision, Key: "shop/checkout@1.0.0", ParentService: "shop/checkout"},
			[]string{"pacto", "fleet", "explain", "shop/checkout"}, "",
		},
		{
			"a target is its own subject",
			Selection{Kind: fleet.KindTarget, Key: "prod/Deployment/checkout", ParentService: "shop/checkout"},
			[]string{"pacto", "fleet", "explain", "prod/Deployment/checkout"}, "",
		},
		{
			"an owner is not a subject",
			Selection{Kind: fleet.KindOwner, Key: "team:x", Label: "x"},
			nil, "press y",
		},
		{
			"a source is not a subject",
			Selection{Kind: fleet.KindSource, Key: "local"},
			nil, "press y",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			why := e.Applies(tt.sel)
			if tt.wantReject != "" {
				if !strings.Contains(why, tt.wantReject) {
					t.Fatalf("e.Applies(%s) = %q, want a reason pointing at the command that does take it", tt.sel.Kind, why)
				}
				return
			}
			if why != "" {
				t.Fatalf("e does not apply to %s: %s", tt.sel.Kind, why)
			}
			if got := e.Argv(c, tt.sel); !slices.Equal(got, tt.want) {
				t.Fatalf("e argv = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFleetExplainRunsTheSubjectItAdvertises closes the gap round 1 was about:
// a verb whose Argv and Run each decide the argument for themselves drifts, and
// the yanked line stops being the line that ran. Both read explainSubject, and
// the output screen's title is built from it too, so the reader is never told
// they are reading an explanation of the revision.
func TestFleetExplainRunsTheSubjectItAdvertises(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	if ref.ParentService == "" {
		t.Fatal("the fixture revision carries no ParentService, so this test would verify nothing")
	}
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if sel.ParentService != ref.ParentService {
		t.Fatalf("resolveSelection dropped ParentService: got %q, want %q", sel.ParentService, ref.ParentService)
	}

	// The advertised line and the screen the verb opens have to name the same
	// subject, and it has to be one the fleet can actually answer for.
	argv := verbBoundTo(t, c, "e").Argv(c, sel)
	subject := argv[len(argv)-1]
	if subject == sel.Key {
		t.Fatalf("e advertises the revision key %q, which Query.Explain never resolves", subject)
	}
	if _, err := c.Query.Explain(subject); err != nil {
		t.Fatalf("e advertises %q, which the fleet cannot explain: %v", subject, err)
	}

	o := readVerbPush(t, verbFleetExplain(c, sel))().(pushMsg).s.(*outputScreen)
	if !strings.Contains(o.Title(), subject) {
		t.Fatalf("the output screen is titled %q, which does not name the subject %q it explained", o.Title(), subject)
	}
}

// TestUnarmedComparisonVerbsAdvertiseTheirMetavar pins d and i. Substituting the
// current selection for the missing left-hand side produced "pacto diff X X",
// which runs, exits 0 and reports NON_BREAKING: a bundle compared with itself,
// dressed as an answer. A placeholder line fails when pasted, which is the
// honest outcome for a command whose other half has not been chosen yet.
func TestUnarmedComparisonVerbsAdvertiseTheirMetavar(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Ref: "/tmp/svc", Local: true}
	armed := Selection{Kind: fleet.KindRevision, Key: "svc@0.9.0", Ref: "/tmp/svc-old", Local: true}

	for key, command := range map[string]string{"d": "diff", "i": "impact"} {
		t.Run(command, func(t *testing.T) {
			v := verbBoundTo(t, c, key)

			c.pendingDiff, c.pendingImpact = Selection{}, Selection{}
			want := []string{"pacto", command, oldPlaceholder, sel.Ref}
			if got := v.Argv(c, sel); !slices.Equal(got, want) {
				t.Fatalf("un-armed %s argv = %v, want %v", key, got, want)
			}

			// Armed, the line names the real left-hand side: the placeholder
			// stands in for the missing half only, not for both halves always.
			c.pendingDiff, c.pendingImpact = armed, armed
			want = []string{"pacto", command, armed.Ref, sel.Ref}
			if got := v.Argv(c, sel); !slices.Equal(got, want) {
				t.Fatalf("armed %s argv = %v, want %v", key, got, want)
			}
		})
	}
}

func TestDispatchIgnoresAnUnboundKey(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	if _, handled := dispatchVerb(c, s, tea.KeyPressMsg{Code: 'Z', Text: "Z"}); handled {
		t.Fatal("an unbound key was consumed")
	}
}

// countingScreen reports how many times dispatch asked it what is selected.
type countingScreen struct {
	ref  fleet.EntityRef
	asks int
}

func (s *countingScreen) Title() string                              { return "Counting" }
func (s *countingScreen) Update(*Context, tea.Msg) (screen, tea.Cmd) { return s, nil }
func (s *countingScreen) View(*Context) string                       { return "" }
func (s *countingScreen) selected() (fleet.EntityRef, bool) {
	s.asks++
	return s.ref, true
}

// TestDispatchResolvesTheSelectionOnlyForABoundKey pins A18. Resolving first
// meant every j, k, g and stray keystroke on a list ran Query.EntityDetail and
// its JSON deep clone — about 1.9 ms of work thrown away per press.
func TestDispatchResolvesTheSelectionOnlyForABoundKey(t *testing.T) {
	c := newLoadedContext(t)
	s := &countingScreen{ref: firstEntityOfKind(t, c, fleet.KindService)}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'j', Text: "j"}, {Code: 'k', Text: "k"}, {Code: 'Z', Text: "Z"},
	} {
		if _, handled := dispatchVerb(c, s, k); handled {
			t.Fatalf("%s is not a verb but was consumed", k.String())
		}
	}
	if s.asks != 0 {
		t.Fatalf("unbound keys resolved the selection %d times, want 0", s.asks)
	}

	if _, handled := dispatchVerb(c, s, tea.KeyPressMsg{Code: 'v', Text: "v"}); !handled {
		t.Fatal("v was not handled")
	}
	if s.asks != 1 {
		t.Fatalf("v resolved the selection %d times, want exactly 1", s.asks)
	}
}

func TestDispatchWithNothingSelectedSetsAStatusInsteadOfRunning(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = nil
	l.tbl.SetRows(nil)
	cmd, handled := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'v', Text: "v"})
	if !handled {
		t.Fatal("v was not handled")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("got %T, want statusMsg", cmd())
	}
	if msg.text == "" {
		t.Fatal("the status message is empty")
	}
}

func TestDiffNeedsTwoSelections(t *testing.T) {
	c := newLoadedContext(t)
	// Get a service with a bundle ref explicitly
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if sel.Ref == "" {
		t.Fatal("the fixture revision resolved to no bundle ref, so this test would verify nothing")
	}

	l := newListScreen(c).(*listScreen)
	// The session lands on Services, so switch to the Revisions tab first.
	for i, k := range l.kinds() {
		if k == fleet.KindRevision {
			l.kindIx = i
		}
	}
	l.refresh(c)
	// Find the revision in the list and set the cursor on it
	found := false
	for i, e := range l.entities {
		if e.Kind == fleet.KindRevision && e.Key == ref.Key {
			l.tbl.SetCursor(i)
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the revision is not in the list")
	}

	// The first d arms the comparison and says so.
	cmd, _ := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if _, ok := cmd().(statusMsg); !ok {
		t.Fatalf("the first d produced %T, want a statusMsg arming the diff", cmd())
	}
	if c.pendingDiff.Ref == "" {
		t.Fatal("the first d did not record the left-hand side")
	}
	// The second d runs it and disarms.
	cmd, _ = dispatchVerb(c, l, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("the second d produced no command")
	}
	if c.pendingDiff.Ref != "" {
		t.Fatal("the pending diff was not cleared after running")
	}
}

func TestRunReadOpensAnOutputScreen(t *testing.T) {
	c := newLoadedContext(t)
	ran := make(chan struct{})
	cmd := runRead(c, "test", func(o *outputScreen) error {
		emit(c, o, "hello\nworld")
		close(ran)
		return nil
	})
	// Sequenced, not batched: batched, the worker can beat its own push, and
	// Model.Update routes every outputLineMsg to the top screen only, so a fast
	// verb loses its whole output behind a spinner that never stops.
	if _, batched := cmd().(tea.BatchMsg); batched {
		t.Fatal("runRead batched the push with the worker; they race and the fast verb loses its output")
	}
	m, ok := readVerbPush(t, cmd)().(pushMsg)
	if !ok {
		t.Fatalf("the first sequenced command produced %T, want a pushMsg", readVerbPush(t, cmd)())
	}
	if _, isOutput := m.s.(*outputScreen); !isOutput {
		t.Fatalf("runRead pushed %T, want an *outputScreen", m.s)
	}
	readVerbWorker(t, cmd)()
	<-ran
}

func TestScreenLocalKeysDoNotShadowVerbs(t *testing.T) {
	c := newLoadedContext(t)
	verbs := map[string]bool{}
	for _, v := range verbList(c) {
		verbs[v.Key] = true
	}
	// Every key a screen handles itself, gathered by reading the switch
	// statements. Keep this list in step with them.
	local := map[string][]string{
		"list":      {"/", "a", "tab", "shift+tab", "enter", "esc"},
		"attention": {"tab", "shift+tab", "enter"},
		"graph":     {"tab", "+", "=", "-", "_"},
	}
	for screen, keys := range local {
		for _, k := range keys {
			if verbs[k] {
				t.Errorf("%s handles %q locally, but it is also a verb key", screen, k)
			}
		}
	}
}

// TestVerbCommandsOnAZeroContext pins the contract internal/cli depends on: the
// boundary test calls this from a package that has no fleet to build a Context
// from, so a zero one has to be enough. A panic here fails the test on its own,
// with a stack that says which field was read; recovering it would throw that
// away and report less.
func TestVerbCommandsOnAZeroContext(t *testing.T) {
	cmds := VerbCommands()
	if len(cmds) == 0 {
		t.Fatal("VerbCommands returned an empty list, verb table is broken")
	}
	for _, c := range cmds {
		if c == "" || strings.Contains(c, "pacto") {
			t.Errorf("%q is not a cobra path; commandPath must strip argv[0]", c)
		}
	}
}

func TestCommandPathDefensiveChecks(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"empty", []string{}, ""},
		{"too short", []string{"pacto"}, ""},
		{"wrong prefix", []string{"not-pacto", "validate"}, ""},
		{"simple command", []string{"pacto", "validate"}, "validate"},
		{"fleet subcommand", []string{"pacto", "fleet", "get"}, "fleet get"},
		{"non-subcommand", []string{"pacto", "validate", "ref"}, "validate"},
		// A yanked fleet line now ends in the session's source flags; the path is
		// still the two words before them.
		{"source flags after the positional", []string{"pacto", "fleet", "get", "svc", "--k8s=true", "--local=a"}, "fleet get"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandPath(tt.argv)
			if got != tt.want {
				t.Errorf("commandPath(%v) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

// TestAReloadDoesNotRaceALiveReadVerb drives the interleaving the -race gate
// could never see on its own: two verb workers on their own goroutines while
// the event loop installs a new snapshot over the Context they are reading.
// Nothing synchronises the two halves — a handshake between them would create
// the happens-before edge that hides the very race this exists to catch — so
// the goroutines are started first, the writes run on the test goroutine and
// the join is at the end.
//
// e and i are the whole population: they are the only tea.Cmd closures that
// read a Context field Model.Update reassigns (Query and Snapshot).
func TestAReloadDoesNotRaceALiveReadVerb(t *testing.T) {
	m := New(Options{Svc: app.NewService(nil, nil), Exe: "/usr/bin/pacto"})
	m.Update(snapshotMsg{snap: testSnapshot(t)})

	ref := firstEntityOfKind(t, m.ctx, fleet.KindService)
	sel, err := resolveSelection(m.ctx, ref)
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	// i only runs once its left-hand side is armed.
	m.ctx.pendingImpact = sel

	workers := []tea.Cmd{
		readVerbWorker(t, verbFleetExplain(m.ctx, sel)),
		readVerbWorker(t, verbImpact(m.ctx, sel)),
	}
	fresh := testSnapshot(t)

	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w()
		}()
	}
	for range 200 {
		m.Update(snapshotMsg{snap: fresh})
	}
	wg.Wait()
}
