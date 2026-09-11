package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestOutputRingBufferIsBounded(t *testing.T) {
	o := newOutputScreen("Validate")
	for i := 0; i < outputMaxLines+500; i++ {
		o.append(fmt.Sprintf("line %d", i))
	}
	if len(o.lines) != outputMaxLines {
		t.Fatalf("buffer holds %d lines, want it capped at %d", len(o.lines), outputMaxLines)
	}
	if o.lines[0] == "line 0" {
		t.Fatal("the ring buffer kept the oldest line; it must drop from the front")
	}
	last := o.lines[len(o.lines)-1]
	if want := fmt.Sprintf("line %d", outputMaxLines+499); last != want {
		t.Fatalf("last line = %q, want %q", last, want)
	}
	if o.dropped == 0 {
		t.Fatal("dropped lines were not counted")
	}
}

func TestOutputSaysWhenLinesWereDropped(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")
	for i := 0; i < outputMaxLines+1; i++ {
		o.append("x")
	}
	if !strings.Contains(o.View(c), "earlier line") {
		t.Fatalf("a truncated buffer must say so:\n%s", o.View(c))
	}
}

func TestOutputIgnoresMessagesFromAPreviousRun(t *testing.T) {
	// Each run gets a fresh id so a superseded run's late messages are dropped
	// instead of landing in the current pane.
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")
	o.id = 7
	next, _ := o.Update(c, outputLineMsg{id: 6, line: "stale"})
	if strings.Contains(next.View(c), "stale") {
		t.Fatal("output from a superseded run was accepted")
	}
	next, _ = next.Update(c, outputLineMsg{id: 7, line: "fresh"})
	if !strings.Contains(next.View(c), "fresh") {
		t.Fatal("output from the current run was rejected")
	}
}

func TestOutputReportsSuccessAndFailure(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")
	done, _ := o.Update(c, outputDoneMsg{id: o.id})
	if !strings.Contains(done.View(c), "done") {
		t.Fatalf("a successful run says nothing:\n%s", done.View(c))
	}
	o2 := newOutputScreen("Validate")
	failed, _ := o2.Update(c, outputDoneMsg{id: o2.id, err: errBoom})
	if !strings.Contains(failed.View(c), "boom") {
		t.Fatalf("a failed run hides the error:\n%s", failed.View(c))
	}
}

func TestOutputIgnoresStaleOutputDoneMsg(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")
	o.id = 10
	next, _ := o.Update(c, outputDoneMsg{id: 9})
	s, ok := next.(*outputScreen)
	if !ok {
		t.Fatal("Update returned wrong type")
	}
	if !s.running {
		t.Fatal("screen stopped running from a stale outputDoneMsg")
	}
	if s.done {
		t.Fatal("screen marked done from a stale outputDoneMsg")
	}
	if strings.Contains(s.View(c), "done") {
		t.Fatalf("screen shows done from a stale outputDoneMsg:\n%s", s.View(c))
	}
}

func TestOutputShowsProgressWhileRunning(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Graph")
	next, _ := o.Update(c, depResolvedMsg{id: o.id})
	if !strings.Contains(next.View(c), "1 ") {
		t.Fatalf("dependency progress is not shown:\n%s", next.View(c))
	}
}

func TestOutputIgnoresStaleDepResolvedMsg(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Graph")
	o.id = 20
	next, _ := o.Update(c, depResolvedMsg{id: 19})
	s, ok := next.(*outputScreen)
	if !ok {
		t.Fatal("Update returned wrong type")
	}
	if s.deps != 0 {
		t.Fatalf("dependency counter moved from stale depResolvedMsg: got %d, want 0", s.deps)
	}
	next, _ = next.Update(c, depResolvedMsg{id: 20})
	s = next.(*outputScreen)
	if s.deps != 1 {
		t.Fatalf("dependency counter did not move from current depResolvedMsg: got %d, want 1", s.deps)
	}
}

func TestOutputDistinctIdsPerScreen(t *testing.T) {
	// Two consecutive newOutputScreen calls must get different ids. A stale
	// run's late output must not land in the next run's pane.
	o1 := newOutputScreen("First")
	o2 := newOutputScreen("Second")
	if o1.id == o2.id {
		t.Fatalf("two screens got the same id %d; distinct runs need distinct ids", o1.id)
	}
}

func TestOutputContentWithoutDrops(t *testing.T) {
	o := newOutputScreen("Test")
	o.append("line 1")
	o.append("line 2")
	content := o.content()
	if strings.Contains(content, "earlier line") {
		t.Fatalf("content shows drop warning when nothing was dropped:\n%s", content)
	}
	if !strings.Contains(content, "line 1") || !strings.Contains(content, "line 2") {
		t.Fatalf("content missing lines:\n%s", content)
	}
}

func TestOutputContentWithDrops(t *testing.T) {
	o := newOutputScreen("Test")
	for i := 0; i < outputMaxLines+100; i++ {
		o.append(fmt.Sprintf("line %d", i))
	}
	content := o.content()
	if !strings.Contains(content, "earlier line") {
		t.Fatalf("content does not show drop warning when lines were dropped:\n%s", content)
	}
}

func TestOutputTitle(t *testing.T) {
	o := newOutputScreen("Validate")
	if got := o.Title(); got != "Validate" {
		t.Fatalf("Title() = %q, want %q", got, "Validate")
	}
}

func TestOutputResizeAdjustsViewport(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Test")
	o.append("test line")
	c.Width, c.Height = 80, 24
	o.resize(c)
	if o.vp.Width() != 80 {
		t.Fatalf("viewport width = %d, want 80", o.vp.Width())
	}
	expectedHeight := 20
	if o.vp.Height() != expectedHeight {
		t.Fatalf("viewport height = %d, want %d", o.vp.Height(), expectedHeight)
	}
}

func TestOutputResizeMinimumHeight(t *testing.T) {
	c := newLoadedContext(t)
	c.Width, c.Height = 50, 2
	o := newOutputScreen("Test")
	o.resize(c)
	if o.vp.Height() < 3 {
		t.Fatalf("viewport height = %d, want at least 3", o.vp.Height())
	}
}

func TestOutputDelegatesToViewport(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Test")
	for i := 0; i < 50; i++ {
		o.append(fmt.Sprintf("line %d", i))
	}
	o.resize(c)
	offsetBefore := o.vp.YOffset()
	next, _ := o.Update(c, tea.KeyPressMsg{Code: tea.KeyDown})
	if next != o {
		t.Fatal("viewport message changed the screen")
	}
	offsetAfter := o.vp.YOffset()
	if offsetAfter == offsetBefore {
		t.Fatalf("viewport did not scroll: offset stayed at %d", offsetBefore)
	}
}

func TestOutputViewBeforeUpdate(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")
	view := o.View(c)
	if view == "" {
		t.Fatal("View before any Update returned empty string")
	}
	if !strings.Contains(view, "running") {
		t.Fatalf("View before any Update does not show running status:\n%s", view)
	}
}

// TestOutputPaneStopsAnimatingWhenTheRunEnds is the whole point of routing the
// pane through the root clock: a finished pane left on screen must schedule no
// further frames. It used to re-arm a spinner.TickMsg unconditionally, so it
// repainted the joined buffer at 10 FPS for as long as the reader left it up.
func TestOutputPaneStopsAnimatingWhenTheRunEnds(t *testing.T) {
	c := newLoadedContext(t)
	c.Anim = true
	o := newOutputScreen("Validate")
	if !o.animating(c) {
		t.Fatal("a running pane has a spinner to turn")
	}
	next, cmd := o.Update(c, outputDoneMsg{id: o.id})
	if cmd != nil {
		t.Fatalf("the pane scheduled its own frame: %T", cmd())
	}
	if next.(*outputScreen).animating(c) {
		t.Fatal("a finished pane still asks for frames")
	}
}

// TestOutputPaneObeysTheAnimationSwitch covers the second half of the same fix:
// the spinner is drawn from Context.Frame, so --no-anim reaches it like it
// reaches every other screen.
func TestOutputPaneObeysTheAnimationSwitch(t *testing.T) {
	c := newLoadedContext(t)
	o := newOutputScreen("Validate")

	c.Anim = false
	if got := o.View(c); strings.Contains(got, spinnerAt(0)) {
		t.Fatalf("the pane spun with animation off:\n%s", got)
	}

	c.Anim = true
	first := o.View(c)
	if !strings.Contains(first, spinnerAt(c.Frame)) {
		t.Fatalf("the pane did not draw the shared spinner glyph:\n%s", first)
	}
	c.Frame++
	if second := o.View(c); second == first {
		t.Fatal("the pane rendered an identical frame after the clock advanced")
	}
}
