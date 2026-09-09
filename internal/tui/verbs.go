package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Verb is one keybound action. Argv is the invocation a reader could have typed
// instead — it drives the yank verb and the write confirmation, so it must
// always be the real command, never an approximation.
type Verb struct {
	Key     string
	Help    string
	Write   bool
	Applies func(sel Selection) bool
	Argv    func(c *Context, sel Selection) []string
	Run     func(c *Context, sel Selection) tea.Cmd
}

// hasBundle is the Applies predicate for every verb that needs a bundle.
func hasBundle(sel Selection) bool { return sel.Ref != "" }

// always is the Applies predicate for verbs that work on any selection.
func always(Selection) bool { return true }

// verbList is the verb table. It drives dispatch, the help screen and the yank
// verb from one definition, so none of the three can drift.
func verbList(c *Context) []Verb {
	vs := []Verb{
		{
			Key: "v", Help: "validate the selected bundle", Applies: hasBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "validate", s.Ref} },
			Run:  verbValidate,
		},
		{
			Key: "E", Help: "explain the selected bundle", Applies: hasBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "explain", s.Ref} },
			Run:  verbExplainLocal,
		},
		{
			Key: "e", Help: "explain the selection from the fleet's point of view", Applies: always,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "fleet", "explain", s.Key} },
			Run:  verbFleetExplain,
		},
		{
			Key: "l", Help: "check the selected bundle's lock file", Applies: hasBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "lock", "--check", s.Ref} },
			Run:  verbLockCheck,
		},
		{
			Key: "d", Help: "diff two selections (press twice)", Applies: hasBundle,
			Argv: func(c *Context, s Selection) []string {
				return []string{"pacto", "diff", orSelf(c.pendingDiff.Ref, s.Ref), s.Ref}
			},
			Run: verbDiff,
		},
		{
			Key: "i", Help: "impact of one selection on another (press twice)", Applies: hasBundle,
			Argv: func(c *Context, s Selection) []string {
				return []string{"pacto", "impact", orSelf(c.pendingImpact.Ref, s.Ref), s.Ref}
			},
			Run: verbImpact,
		},
		{
			Key: "g", Help: "open the selection's neighborhood graph", Applies: always,
			Argv: func(_ *Context, s Selection) []string {
				return []string{"pacto", "fleet", "graph", string(s.Kind), s.Key}
			},
			// Navigation rather than output, but it lives in the table so the
			// help screen and the yank verb see it like everything else.
			Run: func(c *Context, s Selection) tea.Cmd {
				return push(newGraphScreen(c, fleet.EntityRef{
					Kind:    s.Kind,
					Key:     s.Key,
					Label:   s.Label,
					Version: s.Version,
				}))
			},
		},
	}
	if c.ReadOnly {
		return vs
	}
	return append(vs, writeVerbs()...)
}

// writeVerbs returns the verbs that change something; filled in by Task 16.
func writeVerbs() []Verb { return nil }

// orSelf substitutes b when a is empty, so an un-armed two-selection verb still
// yanks a sensible command rather than one with a hole in it.
func orSelf(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// dispatchVerb routes a key to a verb. It is called only by the screens that
// carry a selection, so a confirmation screen or a focused filter never runs a
// verb by accident.
func dispatchVerb(c *Context, s screen, k tea.KeyPressMsg) (tea.Cmd, bool) {
	sel, ok := selectionOf(c, s)
	key := k.String()
	for _, v := range verbList(c) {
		if v.Key != key {
			continue
		}
		if !ok {
			return status("nothing selected"), true
		}
		if !v.Applies(sel) {
			return status(fmt.Sprintf("%s does not apply to a %s", v.Help, sel.Kind)), true
		}
		return v.Run(c, sel), true
	}
	return nil, false
}

// selectionOf resolves the screen's highlighted entity into a runnable
// Selection. A screen with no selection reports ok=false.
func selectionOf(c *Context, s screen) (Selection, bool) {
	sr, ok := s.(interface {
		selected() (fleet.EntityRef, bool)
	})
	if !ok {
		return Selection{}, false
	}
	ref, ok := sr.selected()
	if !ok {
		return Selection{}, false
	}
	sel, err := resolveSelection(c, ref)
	if err != nil {
		return Selection{}, false
	}
	return sel, true
}

func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

// runRead opens an output screen and runs fn against it on a background
// goroutine. fn appends through the screen's id so late output from a
// superseded run is discarded rather than mixed in.
func runRead(c *Context, title string, fn func(o *outputScreen) error) tea.Cmd {
	o := newOutputScreen(title)
	return tea.Batch(
		push(o),
		o.sp.Tick,
		func() tea.Msg {
			err := fn(o)
			c.Send.send(outputDoneMsg{id: o.id, err: err})
			return nil
		},
	)
}

// emit posts one block of text into an output screen, one line per message so
// the pane fills progressively rather than all at once at the end.
func emit(c *Context, o *outputScreen, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		c.Send.send(outputLineMsg{id: o.id, line: line})
	}
}

// onDep is the only safe body for an app-layer OnDepResolved hook: it fires
// from several goroutines at once, so it may do nothing but post a message.
// The id is what keeps a superseded run's late progress out of the current pane.
func onDep(c *Context, id int) func() {
	return func() { c.Send.send(depResolvedMsg{id: id}) }
}

func verbValidate(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "validate "+sel.Label, func(o *outputScreen) error {
		res, err := c.Svc.Validate(c.Ctx, app.ValidateOptions{Path: sel.Ref})
		if err != nil {
			return err
		}
		// Validate never returns an error for a bad contract; the verdict is in
		// the result. Reporting err alone would call every invalid bundle fine.
		emit(c, o, renderValidate(res))
		return nil
	})
}

func verbExplainLocal(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "explain "+sel.Label, func(o *outputScreen) error {
		res, err := c.Svc.Explain(c.Ctx, app.ExplainOptions{Path: sel.Ref})
		if err != nil {
			return err
		}
		emit(c, o, renderExplain(res))
		return nil
	})
}

func verbFleetExplain(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "fleet explain "+sel.Label, func(o *outputScreen) error {
		res, err := c.Query.Explain(sel.Key)
		if err != nil {
			return err
		}
		emit(c, o, renderFleetExplain(res))
		return nil
	})
}

func verbLockCheck(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "lock --check "+sel.Label, func(o *outputScreen) error {
		res, err := c.Svc.Lock(c.Ctx, app.LockOptions{
			Path: sel.Ref, Check: true, OnDepResolved: onDep(c, o.id),
		})
		if err != nil {
			return err
		}
		emit(c, o, renderLock(res))
		return nil
	})
}

func verbDiff(c *Context, sel Selection) tea.Cmd {
	if c.pendingDiff.Ref == "" {
		c.pendingDiff = sel
		return status("diff armed on " + sel.Label + " — select the other side and press d again")
	}
	old := c.pendingDiff
	c.pendingDiff = Selection{}
	return runRead(c, "diff", func(o *outputScreen) error {
		res, err := c.Svc.Diff(c.Ctx, app.DiffOptions{OldPath: old.Ref, NewPath: sel.Ref})
		if err != nil {
			return err
		}
		emit(c, o, renderDiff(res))
		return nil
	})
}

func verbImpact(c *Context, sel Selection) tea.Cmd {
	if c.pendingImpact.Ref == "" {
		c.pendingImpact = sel
		return status("impact armed on " + sel.Label + " — select the new revision and press i again")
	}
	old := c.pendingImpact
	c.pendingImpact = Selection{}
	return runRead(c, "impact", func(o *outputScreen) error {
		// ImpactWithSnapshot binds the answer to the snapshot on screen. Impact
		// would build a second one, and then the blast radius shown would not be
		// the blast radius over the fleet the reader is looking at.
		res, err := c.Svc.ImpactWithSnapshot(c.Ctx, app.ImpactOptions{
			OldPath: old.Ref, NewPath: sel.Ref, Fleet: c.Fleet,
		}, c.Snapshot)
		if err != nil {
			return err
		}
		emit(c, o, renderImpact(res))
		return nil
	})
}
