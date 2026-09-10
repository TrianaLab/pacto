package tui

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// hostilePayload is one string carrying every byte class the renderer that
// composes the frame acts on rather than shows: a carriage return, a 7-bit CSI
// erase-line, an 8-bit CSI and an 8-bit OSC, DEL, a right-to-left override, a
// bidi isolate and a newline. Nothing between a contract on disk and this
// package validates content -- internal/fleetsrc/local.go calls contract.Parse
// and nothing else, and the k8s source copies custom-resource values through
// verbatim -- so every one of these can be in a service name.
//
// It is deliberately ONE payload planted everywhere rather than a payload per
// field. The defect this file exists to close is not any single field: it is
// that the set of render sites is bigger than the set anyone can enumerate, and
// three attempts to enumerate it produced three wrong answers.
const hostilePayload = "pwn\r\x1b[2K\u009b3D\u009d\x7f\u202e\u2066\nEVIL"

// hostileEscaped is what safeText makes of it, spelled out rather than computed
// so this file does not check the sanitiser against itself.
const hostileEscaped = "pwn^M^[[2K" + `\x9b` + "3D" + `\x9d` + "^?" + `\u202e\u2066` + "^JEVIL"

// hostileMarker is the head of hostileEscaped, short enough to survive a table
// column. A case that claims the payload reached the frame looks for this.
const hostileMarker = "pwn^M^[[2K"

// sgr matches the only escape sequence this package puts in a frame on purpose:
// a Select Graphic Rendition, which is what lipgloss emits for a colour, bold
// or faint. Everything else that reaches the terminal came from the data.
var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// terminalActsOn reports whether r is a character a terminal obeys rather than
// prints. It repeats the ranges safeText covers instead of calling isUnsafe:
// a test that shares its predicate with the code under test passes whenever
// both are wrong together.
//
// Newline is the one exemption, because the frame is made of lines. It costs
// nothing: safeText handles LF and CR in the same branch -- caret notation on
// r+0x40, giving ^J and ^M -- so a site that escapes the payload's carriage
// return has necessarily escaped its newline, and a site that does not escape
// is caught on the carriage return. The one shape that would slip past is a
// site that strips CR specifically and passes LF through, which nothing here
// does; assertFrameClean's line check below covers even that.
func terminalActsOn(r rune) bool {
	switch {
	case r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// assertFrameClean is the whole assertion: after this package's own styling is
// removed, no byte the terminal acts on may remain.
func assertFrameClean(t *testing.T, where, frame string) {
	t.Helper()
	stripped := sgr.ReplaceAllString(frame, "")
	for i, r := range stripped {
		if terminalActsOn(r) {
			t.Fatalf("%s: the frame carries U+%04X at byte %d, which the renderer acts on rather than shows:\n%q",
				where, r, i, stripped)
		}
	}
	// The newline exemption, closed. A leaked LF puts whatever followed it in
	// the payload at the start of a line of its own; escaped, "EVIL" can only
	// ever appear after a "^J" on a line that already had text on it.
	for _, line := range strings.Split(stripped, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "EVIL") {
			t.Fatalf("%s: a payload newline broke the frame into a line the data controls:\n%q", where, stripped)
		}
	}
}

// hostileSnapshot is a fleet whose every contract-supplied string carries the
// payload: two services in two domains, one local and one from a registry, with
// owners, interfaces, configurations, policies, capabilities and a dependency
// edge between them, plus two targets with findings and observed runtime.
//
// It is built through fleet.Build over a memory source for the reason
// testSnapshot is: FleetSnapshot's indexes are unexported and only Build fills
// them, so a hand-assembled snapshot would not exercise the queries the screens
// really run.
func hostileSnapshot(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	p := hostilePayload
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	provider := &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name: p + "-provider", Version: "1.0.0",
			Owner: contract.Owner{Team: p},
		},
		Workload:       p,
		Interfaces:     []contract.Interface{{Name: p, Type: p, Ref: p}},
		Configurations: []contract.Configuration{{Name: p, Schema: p}},
		Policies:       []contract.Policy{{Name: p, Schema: p, Target: p}},
		Capabilities:   []contract.Capability{{Type: p, Ref: p}},
	}
	consumer := &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name: p + "-consumer", Version: "2.0.0",
			Owner: contract.Owner{DRI: p},
		},
		Dependencies: []contract.Dependency{
			{Name: p + "-provider", Ref: "oci://" + p + "/provider:1.0.0", Required: true},
		},
	}

	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{
				Bundle:       &contract.Bundle{Contract: provider},
				RequestedRef: "file:///tmp/" + p,
				Digest:       testDigest,
			},
			{
				Bundle:       &contract.Bundle{Contract: consumer},
				Domain:       p,
				RequestedRef: "oci://" + p + "/consumer:2.0.0",
				ResolvedRef:  "oci://" + p + "/consumer@" + anotherDigest,
				Digest:       anotherDigest,
			},
		},
		Targets: []fleet.RawTarget{
			{
				Scope: p, Kind: p, Name: p + "-deploy",
				Service: p + "-provider", Digest: testDigest,
				Compliance: "NonCompliant", EvidenceAt: &now,
				ObservedRuntime: map[string]any{p: p},
				Findings: []finding.Finding{{
					Code: finding.Code(p), Severity: finding.SeverityError,
					Category: finding.CategoryPolicyViolation, Message: p,
				}},
			},
			{
				Scope: p + "-other", Kind: p, Name: p + "-deploy",
				Service: p + "-consumer", Domain: p, Digest: anotherDigest,
				Compliance: "Compliant", EvidenceAt: &now,
			},
		},
	}

	src := fleet.NewMemorySource(p+"-source", p, col)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{
		Now: func() time.Time { return now },
	}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return snap
}

// hostileModel is a root model over the hostile fleet, sized like a terminal.
// The walk goes through the root rather than through each screen's View so the
// header and the footer are in every frame -- the breadcrumb is built from
// titles, and the footer from an error or a status line, and all three are
// render sites too.
func hostileModel(t *testing.T) *Model {
	t.Helper()
	m := New(testOptions())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(snapshotMsg{snap: hostileSnapshot(t)})
	return m
}

// frameWith renders the model with s on top.
func frameWith(m *Model, s screen) string {
	m.Update(pushMsg{s: s})
	out := m.View().Content
	m.Update(popMsg{})
	return out
}

// hostileRef returns the fleet's entity of a given kind. Every one of them
// carries the payload somewhere, because every contract string does.
func hostileRef(t *testing.T, c *Context, kind fleet.EntityKind) fleet.EntityRef {
	t.Helper()
	return firstEntityOfKind(t, c, kind)
}

// hostileErr is a service-layer error quoting untrusted text, which is how the
// two sites found in the last review round leaked: outputScreen.status renders
// err.Error() and four sibling screens render a loadErr the same way. It is a
// real fleet error type rather than errors.New, because the shape that made
// those sites look safe was NotFoundError's %q -- a formatting accident of one
// error type, standing in for a guarantee.
func hostileErr() error {
	return &fleet.AmbiguousError{
		Kind: "service", ID: hostilePayload,
		Matches: []string{hostilePayload, hostilePayload + "-2"},
	}
}

// hostileLine is a line of verb output shaped the way a renderer that forgot
// one safeText call would produce it: this package's own styling on the verdict
// and raw contract text after it. The pane cannot simply escape everything it
// is handed -- renderValidate and its siblings style their verdicts, and
// renderDiff passes pkg/graph's coloured tree through whole -- so this is the
// shape that separates a line that keeps its colour from one that keeps the
// payload's escapes.
func hostileLine() string {
	return okStyle.Render("invalid") + "  " + hostilePayload
}

// hostileCase is one screen, in one state, that has to render clean.
type hostileCase struct {
	name string
	// build returns the screen to put on top. It may also mutate the model, for
	// the footer states that are not a screen at all.
	build func(t *testing.T, m *Model) screen
	// wantPayload is set where the payload must be visible in the frame, so a
	// case cannot pass because the data never arrived.
	wantPayload bool
}

func hostileCases() []hostileCase {
	// Each verb screen is driven in every state its own keys can reach: a list
	// on each kind tab, an attention screen on each category, a graph at each
	// direction. A state a reader can reach is a state the payload can reach.
	return []hostileCase{
		{"loading", func(*testing.T, *Model) screen {
			return loadingScreen{note: hostilePayload}
		}, false},

		{"list", func(_ *testing.T, m *Model) screen {
			return newListScreen(m.ctx)
		}, true},
		{"list on every kind tab", func(_ *testing.T, m *Model) screen {
			l := newListScreen(m.ctx).(*listScreen)
			for range l.kinds() {
				l.Update(m.ctx, tea.KeyPressMsg{Code: tea.KeyTab})
			}
			return l
		}, true},
		{"list with a filter applied", func(_ *testing.T, m *Model) screen {
			l := newListScreen(m.ctx).(*listScreen)
			l.filterText = hostilePayload
			l.refresh(m.ctx)
			return l
		}, true},
		{"list with the filter input focused", func(_ *testing.T, m *Model) screen {
			l := newListScreen(m.ctx).(*listScreen)
			l.typing = true
			l.input.SetValue(hostilePayload)
			return l
		}, false},
		{"list whose query failed", func(_ *testing.T, m *Model) screen {
			l := newListScreen(m.ctx).(*listScreen)
			l.loadErr = hostileErr()
			return l
		}, true},

		{"detail of a service", detailOf(fleet.KindService), true},
		{"detail of a revision", detailOf(fleet.KindRevision), true},
		{"detail of a target", detailOf(fleet.KindTarget), true},
		{"detail of an owner", detailOf(fleet.KindOwner), true},
		{"detail of a source", detailOf(fleet.KindSource), true},
		{"detail whose lookup failed", func(t *testing.T, m *Model) screen {
			d := newDetailScreen(m.ctx, hostileRef(t, m.ctx, fleet.KindService)).(*detailScreen)
			d.loadErr = hostileErr()
			return d
		}, true},

		{"attention", func(_ *testing.T, m *Model) screen {
			return newAttentionScreen(m.ctx)
		}, true},
		{"attention on every category tab", func(_ *testing.T, m *Model) screen {
			a := newAttentionScreen(m.ctx).(*attentionScreen)
			for range attentionCategories() {
				a.Update(m.ctx, tea.KeyPressMsg{Code: tea.KeyTab})
			}
			return a
		}, false},
		{"attention whose query failed", func(_ *testing.T, m *Model) screen {
			a := newAttentionScreen(m.ctx).(*attentionScreen)
			a.loadErr = hostileErr()
			return a
		}, true},

		{"graph of a service", graphOf(fleet.KindService), true},
		{"graph of a revision", graphOf(fleet.KindRevision), true},
		{"graph of a target", graphOf(fleet.KindTarget), true},
		{"graph in every direction and deeper", func(t *testing.T, m *Model) screen {
			g := newGraphScreen(m.ctx, hostileRef(t, m.ctx, fleet.KindService)).(*graphScreen)
			for range graphDirections() {
				g.Update(m.ctx, tea.KeyPressMsg{Code: tea.KeyTab})
			}
			g.Update(m.ctx, tea.KeyPressMsg{Code: '+'})
			return g
		}, true},
		{"graph whose query failed", func(t *testing.T, m *Model) screen {
			g := newGraphScreen(m.ctx, hostileRef(t, m.ctx, fleet.KindService)).(*graphScreen)
			g.loadErr = hostileErr()
			return g
		}, true},

		{"output still running", func(_ *testing.T, m *Model) screen {
			o := newOutputScreen(hostilePayload)
			o.append(hostileLine())
			o.deps = 1
			return o
		}, true},
		{"output that finished", func(_ *testing.T, m *Model) screen {
			o := newOutputScreen(hostilePayload)
			o.append(hostileLine())
			o.done = true
			return o
		}, true},
		{"output that failed", func(_ *testing.T, m *Model) screen {
			o := newOutputScreen(hostilePayload)
			o.append(hostileLine())
			o.done, o.err = true, hostileErr()
			return o
		}, true},
		{"output that dropped lines", func(_ *testing.T, m *Model) screen {
			o := newOutputScreen(hostilePayload)
			for range outputMaxLines + 2 {
				o.append(hostileLine())
			}
			return o
		}, true},

		{"prompt", func(_ *testing.T, m *Model) screen {
			p := &promptScreen{question: hostilePayload}
			p.input.SetValue(hostilePayload)
			return p
		}, true},

		{"confirm", func(_ *testing.T, m *Model) screen {
			return newConfirmScreen(hostilePayload,
				[]string{"pacto", "push", hostilePayload, "-p", hostilePayload}, nil)
		}, true},

		// Neither help case can want the payload: the help screen draws the key
		// list and nothing of the screen underneath it. They are here because the
		// inventory guard requires every screen to be walked, and because "help
		// renders no fleet text" is a fact worth having a test fail on if it ever
		// stops being one.
		{"help over a detail", func(t *testing.T, m *Model) screen {
			return helpScreen{under: newDetailScreen(m.ctx, hostileRef(t, m.ctx, fleet.KindService))}
		}, false},
		{"help over the list", func(_ *testing.T, m *Model) screen {
			return helpScreen{under: newListScreen(m.ctx)}
		}, false},
	}
}

func detailOf(kind fleet.EntityKind) func(*testing.T, *Model) screen {
	return func(t *testing.T, m *Model) screen {
		return newDetailScreen(m.ctx, hostileRef(t, m.ctx, kind))
	}
}

func graphOf(kind fleet.EntityKind) func(*testing.T, *Model) screen {
	return func(t *testing.T, m *Model) screen {
		return newGraphScreen(m.ctx, hostileRef(t, m.ctx, kind))
	}
}

// TestNoScreenLeaksHostileBytes is the class-closing test. It plants one
// payload through the fleet, walks every screen in every state its own keys can
// reach, and asserts on the composed frame.
//
// It is deliberately not a per-site assertion. Three rounds of per-site
// assertions passed while a render site next to the one being asserted on
// leaked, because the assertion knew which sites to look at and the reviewer
// did not have to. This one knows nothing about sites: it renders and reads the
// bytes.
func TestNoScreenLeaksHostileBytes(t *testing.T) {
	for _, tc := range hostileCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := hostileModel(t)
			frame := frameWith(m, tc.build(t, m))
			assertFrameClean(t, tc.name, frame)
			if tc.wantPayload && !strings.Contains(frame, hostileMarker) {
				t.Fatalf("%s: the payload never reached the frame, so a clean frame proves nothing:\n%q",
					tc.name, frame)
			}
		})
	}
}

// TestTheOutputPaneKeepsItsColourWhileCleaningTheData is what stops the pane's
// sanitiser from being "simplified" into safeText. A pane line arrives
// part-rendered, so escaping it whole would silently turn every verdict, every
// diff marker and every coloured tree pkg/graph draws into literal "^[[92m"
// text -- a change no other test in this package would notice, because a frame
// full of escaped escapes is still a clean frame.
func TestTheOutputPaneKeepsItsColourWhileCleaningTheData(t *testing.T) {
	m := hostileModel(t)
	o := newOutputScreen("verdict")
	o.append(hostileLine())
	frame := frameWith(m, o)
	assertFrameClean(t, "the output pane", frame)
	if !strings.Contains(frame, okStyle.Render("invalid")) {
		t.Fatalf("the pane lost the styling the renderer put on its verdict:\n%q", frame)
	}
	if !strings.Contains(frame, hostileMarker) {
		t.Fatalf("the pane never showed the payload:\n%q", frame)
	}
}

// attackModel is the hostile model wired the way a running session is: a real
// app.Service, so a verb that reaches the app layer fails the way it fails in
// production -- with an error naming the ref it could not resolve -- and a
// message sink, so the output a verb posts from its worker goroutine can be
// replayed into the model instead of being dropped by sender's nil guard.
func attackModel(t *testing.T) *Model {
	t.Helper()
	m := hostileModel(t)
	m.ctx.Svc = app.NewService(nil, nil)
	m.ctx.Send = &sender{p: &msgRecorder{}}
	return m
}

// hostileSelection resolves a real row of the hostile fleet into the Selection a
// verb runs against. Built rather than hand-written: sel.Ref comes out of
// bundleRef, and a hand-written one would be testing a string this package
// invented rather than the one the fleet hands it.
func hostileSelection(t *testing.T, m *Model, kind fleet.EntityKind) Selection {
	t.Helper()
	sel, err := resolveSelection(m.ctx, hostileRef(t, m.ctx, kind))
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if !strings.Contains(sel.Ref, hostilePayload) {
		t.Fatalf("the %s selection's ref %q does not carry the payload, so the attack was never delivered", kind, sel.Ref)
	}
	return sel
}

// deliver runs a read verb the way the runtime does -- push the output screen,
// then run the worker, then replay what the worker posted -- and returns the
// frame the reader is left looking at. The worker runs on this goroutine, so
// there is nothing to wait for and nothing to flake on.
func deliver(t *testing.T, m *Model, cmd tea.Cmd) string {
	t.Helper()
	m.Update(readVerbPush(t, cmd)())
	readVerbWorker(t, cmd)()
	rec := m.ctx.Send.p.(*msgRecorder)
	for _, msg := range rec.msgs {
		m.Update(msg)
	}
	rec.msgs = nil
	return m.View().Content
}

// TestAHostileRefCannotForgeAVerbsOutput is the first of the attacks: a service
// whose bundle ref carries the payload, run through the read verbs that put
// that ref into a command and an error message. Each of these fails in the app
// layer -- there is no such directory -- and the failure quotes the ref, which
// is exactly the shape that leaked before: an err.Error() rendered raw.
func TestAHostileRefCannotForgeAVerbsOutput(t *testing.T) {
	for _, key := range []string{"E", "l"} {
		t.Run(key, func(t *testing.T) {
			m := attackModel(t)
			sel := hostileSelection(t, m, fleet.KindService)
			v := verbBoundTo(t, m.ctx, key)
			if why := v.Applies(sel); why != "" {
				t.Fatalf("%q does not apply to the hostile selection, so nothing was attacked: %s", key, why)
			}
			frame := deliver(t, m, v.Run(m.ctx, sel))
			assertFrameClean(t, key, frame)
			if !strings.Contains(frame, hostileMarker) {
				t.Fatalf("%s: the payload never reached the frame:\n%q", key, frame)
			}
		})
	}
}

// TestAHostileRefCannotForgeTheDiffVerb covers d, which is two keypresses: the
// first arms and writes the selection's label into the status line, the second
// runs and reports a failure naming both refs. Both frames are read.
func TestAHostileRefCannotForgeTheDiffVerb(t *testing.T) {
	m := attackModel(t)
	sel := hostileSelection(t, m, fleet.KindService)
	v := verbBoundTo(t, m.ctx, "d")

	m.Update(v.Run(m.ctx, sel)())
	armed := m.View().Content
	assertFrameClean(t, "d armed", armed)
	if !strings.Contains(armed, hostileMarker) {
		t.Fatalf("the arming notice does not name the selection:\n%q", armed)
	}

	frame := deliver(t, m, v.Run(m.ctx, sel))
	assertFrameClean(t, "d run", frame)
	if !strings.Contains(frame, hostileMarker) {
		t.Fatalf("the diff output does not carry the payload:\n%q", frame)
	}
}

// TestFleetExplainCannotForgeItsOutput runs e against every kind it applies to.
//
// The review asked specifically for a fleet.AmbiguousError through e. It cannot
// be produced there: Query.Explain resolves its subject by exact key first
// (query.go:44,533) and explainSubject only ever hands it a canonical one -- a
// service's own key, a revision's ParentService, a target's TargetKey -- so the
// name-matching branch that builds an AmbiguousError is never reached. The
// error still has to render safely, which is what the "output that failed" case
// in the walk asserts with exactly that error; this covers the reachable half.
func TestFleetExplainCannotForgeItsOutput(t *testing.T) {
	for _, kind := range []fleet.EntityKind{fleet.KindService, fleet.KindRevision, fleet.KindTarget} {
		t.Run(string(kind), func(t *testing.T) {
			m := attackModel(t)
			sel := hostileSelection(t, m, kind)
			v := verbBoundTo(t, m.ctx, "e")
			if why := v.Applies(sel); why != "" {
				t.Fatalf("e does not apply to a %s, so nothing was attacked: %s", kind, why)
			}
			frame := deliver(t, m, v.Run(m.ctx, sel))
			assertFrameClean(t, "e on a "+string(kind), frame)
			if !strings.Contains(frame, hostileMarker) {
				t.Fatalf("e on a %s never showed the payload:\n%q", kind, frame)
			}
		})
	}
}

// TestAHostileLabelCannotForgeAWriteGate is the attack that matters most: p and
// G ask a question built from the selection's label, and the answer plus the
// label become the argv on the confirmation screen -- the only gate on a write.
// Both frames are read, and the confirmation is read whole rather than by its
// head, because that is the surface a forgery would be aimed at.
func TestAHostileLabelCannotForgeAWriteGate(t *testing.T) {
	for _, tt := range []struct{ key, answer, plugin string }{
		{"p", "oci://ghcr.io/acme/svc:1.0.0", ""},
		{"G", "schema-infer", "schema-infer"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			if tt.plugin != "" {
				stubPlugin(t, tt.plugin)
			}
			m := attackModel(t)
			sel := hostileSelection(t, m, fleet.KindService)
			if !strings.Contains(sel.Label, hostilePayload) {
				t.Fatalf("the selection's label %q does not carry the payload", sel.Label)
			}
			v := verbBoundTo(t, m.ctx, tt.key)
			if why := v.Applies(sel); why != "" {
				t.Fatalf("%q does not apply to the hostile selection: %s", tt.key, why)
			}

			cmd := v.Run(m.ctx, sel)
			batch := cmdMembers(t, cmd)
			m.Update(batch[0]())
			prompt := m.View().Content
			assertFrameClean(t, tt.key+" prompt", prompt)
			if !strings.Contains(prompt, hostileMarker) {
				t.Fatalf("%s: the prompt never showed the label it is about:\n%q", tt.key, prompt)
			}

			p, ok := m.top().(*promptScreen)
			if !ok {
				t.Fatalf("%s pushed %T, want the prompt", tt.key, m.top())
			}
			m.Update(p.onSubmit(tt.answer)())
			gate := sgr.ReplaceAllString(m.View().Content, "")
			assertFrameClean(t, tt.key+" confirmation", gate)
			if _, ok := m.top().(*confirmScreen); !ok {
				t.Fatalf("%s did not reach the confirmation, so the gate was never rendered", tt.key)
			}
			if !strings.Contains(gate, hostileEscaped) {
				t.Fatalf("%s: the gate does not show the payload escaped in full:\n%q", tt.key, gate)
			}
		})
	}
}

// TestNoVerbBuildsAnEmptyArgvToken closes the one attack the confirmation
// screen cannot defend against by rendering. An empty token is invisible there
// -- strings.Join collapses it into a run of spaces -- so "pacto push ” -p X"
// would read as "pacto push  -p X" and the reader would confirm a command with
// a different shape than the one shown. Nothing can produce one today: every
// verb's Applies rejects an empty Ref, and a prompt refuses an empty answer
// (prompt.go:44). This is what keeps that true, because it is cheaper to hold
// the invariant at the source than to teach the gate to draw a quote.
func TestNoVerbBuildsAnEmptyArgvToken(t *testing.T) {
	m := attackModel(t)
	for _, kind := range []fleet.EntityKind{
		fleet.KindService, fleet.KindRevision, fleet.KindTarget, fleet.KindOwner, fleet.KindSource,
	} {
		sel, err := resolveSelection(m.ctx, hostileRef(t, m.ctx, kind))
		if err != nil {
			t.Fatalf("resolveSelection: %v", err)
		}
		for _, v := range verbList(m.ctx) {
			if v.Applies(sel) != "" {
				continue
			}
			for i, tok := range v.Argv(m.ctx, sel) {
				if tok == "" {
					t.Errorf("%q on a %s builds an empty token at position %d, which the confirmation cannot show",
						v.Key, kind, i)
				}
			}
		}
	}
}

// TestTheFooterCannotBeForged covers the two states that are not a screen: the
// root renders an error and a status line under whatever is on the stack, and
// both are built from fleet text -- a status names a selection's label and an
// error quotes the path or the name that failed.
func TestTheFooterCannotBeForged(t *testing.T) {
	for _, tt := range []struct {
		name  string
		apply func(m *Model)
	}{
		{"an error", func(m *Model) { m.err = hostileErr() }},
		{"a status line", func(m *Model) { m.ctx.Status = hostilePayload }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := hostileModel(t)
			tt.apply(m)
			frame := m.View().Content
			assertFrameClean(t, tt.name, frame)
			if !strings.Contains(frame, hostileMarker) {
				t.Fatalf("the payload never reached the footer:\n%q", frame)
			}
		})
	}
}

// TestTheConfirmationShowsTheWholeEscapedPayload is the anti-vacuity check the
// walk cannot make. Every other frame truncates -- a table column is 34 cells,
// a viewport clips -- so the walk can only look for the head of the escaped
// payload. The confirmation screen wraps nothing and clips nothing, and it is
// the surface that matters: it is the only gate on push, pull, lock --update
// and generate. So this is where the payload is checked whole, byte for byte,
// including the ^J that stands for the newline.
func TestTheConfirmationShowsTheWholeEscapedPayload(t *testing.T) {
	m := hostileModel(t)
	frame := frameWith(m, newConfirmScreen("Push "+hostilePayload+"?",
		[]string{"pacto", "push", hostilePayload}, nil))
	stripped := sgr.ReplaceAllString(frame, "")
	if !strings.Contains(stripped, hostileEscaped) {
		t.Fatalf("the confirmation does not show the payload escaped in full:\n%q", stripped)
	}
	if !strings.Contains(stripped, "pacto push") {
		t.Fatalf("the confirmation no longer shows the command it will run:\n%q", stripped)
	}
}

// TestEveryScreenIsInTheHostileWalk is what makes the walk keep up. A screen
// added next month is a set of render sites the walk would silently not visit,
// and a walk that quietly covers less than it did is worse than no walk: it
// still passes.
//
// The inventory is read out of the source rather than listed here, because a
// list is the thing that goes stale.
func TestEveryScreenIsInTheHostileWalk(t *testing.T) {
	walked := map[string]bool{}
	m := hostileModel(t)
	for _, tc := range hostileCases() {
		s := tc.build(t, m)
		ty := reflect.TypeOf(s)
		for ty.Kind() == reflect.Pointer {
			ty = ty.Elem()
		}
		walked[ty.Name()] = true
	}
	for _, name := range screenTypesInSource(t) {
		if !walked[name] {
			t.Errorf("%s renders a frame but TestNoScreenLeaksHostileBytes never walks it; "+
				"add a case to hostileCases()", name)
		}
	}
}

// screenTypesInSource returns every type in this package with a
// View(*Context) string method, which is the signature that makes something a
// screen. Test files are excluded: a fake screen in a test is not a surface a
// reader ever sees.
func screenTypesInSource(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "View" || !viewsAContext(fn) {
				continue
			}
			out = append(out, receiverTypeName(fn))
		}
	}
	if len(out) == 0 {
		t.Fatal("found no screens in the package source, so this guard is asserting nothing")
	}
	return out
}

// viewsAContext reports whether fn takes exactly one *Context, which is what
// separates a screen's View from the root Model's.
func viewsAContext(fn *ast.FuncDecl) bool {
	if len(fn.Type.Params.List) != 1 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Context"
}

// receiverTypeName is the receiver's type name with any pointer stripped.
func receiverTypeName(fn *ast.FuncDecl) string {
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
