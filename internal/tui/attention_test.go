package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestAttentionListsItems(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	if s.Title() != "Attention" {
		t.Fatalf("Title() = %q", s.Title())
	}
	out := s.View(c)
	if out == "" {
		t.Fatal("attention screen rendered nothing")
	}
	if !strings.Contains(out, "all") {
		t.Fatalf("view should contain the first category tab 'all':\n%s", out)
	}
	if len(s.items) == 0 {
		t.Fatalf("test fixture produces no attention items")
	}
	firstItem := s.items[0]
	if !strings.Contains(out, firstItem.Service) && !strings.Contains(out, firstItem.Summary) {
		t.Fatalf("view should contain text from the first attention item (service=%q, summary=%q):\n%s",
			firstItem.Service, firstItem.Summary, out)
	}
}

func TestAttentionCategoryFilterCyclesWithoutMutatingThePackageVar(t *testing.T) {
	before := append([]string(nil), fleet.AttentionCategories...)
	c := newLoadedContext(t)
	var s screen = newAttentionScreen(c)
	for i := 0; i < len(before)+2; i++ {
		s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	for i, v := range fleet.AttentionCategories {
		if v != before[i] {
			t.Fatalf("fleet.AttentionCategories was mutated at %d: %q != %q", i, v, before[i])
		}
	}
}

func TestAttentionSurfacesAQueryError(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.loadErr = errBoom
	if !strings.Contains(s.View(c), "boom") {
		t.Fatalf("attention screen hides the query error:\n%s", s.View(c))
	}
}

func TestAttentionEmptyStateIsDistinctFromAnError(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.items, s.loadErr = nil, nil
	out := s.View(c)
	if strings.Contains(out, "failed") {
		t.Fatalf("an empty result must not read as a failure:\n%s", out)
	}
	if out == "" {
		t.Fatal("an empty result must still say something")
	}
}

func TestAttentionSelectedReturnsTheHighlightedItem(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	if len(s.items) == 0 {
		t.Fatalf("no attention items in test snapshot")
	}
	ref, ok := s.selected()
	if !ok {
		t.Fatal("nothing selected in a non-empty attention list")
	}
	if ref.Key == "" {
		t.Fatal("selected ref has no key")
	}
}

func TestAttentionSelectedOnEmptyListReportsNothing(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.items = nil
	if _, ok := s.selected(); ok {
		t.Fatal("an empty attention list must not report a selection")
	}
}

func TestAttentionTabsCycle(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newAttentionScreen(c)
	first := s.(*attentionScreen).catIx
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	if s.(*attentionScreen).catIx == first {
		t.Fatal("tab did not advance the category")
	}
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if s.(*attentionScreen).catIx != first {
		t.Fatal("shift+tab did not go back")
	}
}

func TestAttentionHandlesWindowSizeMsg(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newAttentionScreen(c)
	c.Width, c.Height = 120, 40
	s, _ = s.Update(c, tea.WindowSizeMsg{Width: 120, Height: 40})
	a := s.(*attentionScreen)
	if a.tbl.Width() != 120 {
		t.Fatalf("table width = %d, want 120", a.tbl.Width())
	}
}

func TestAttentionNextStepShowsReasonAndRemedy(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.items = []fleet.AttentionItem{
		{
			Entity:   fleet.EntityRef{Kind: fleet.KindService, Key: "test", Label: "test"},
			Reason:   "test reason",
			NextStep: "test remedy",
		},
	}
	s.tbl.SetCursor(0)
	out := s.nextStep()
	if !strings.Contains(out, "test reason") {
		t.Fatalf("nextStep should show reason: %q", out)
	}
	if !strings.Contains(out, "test remedy") {
		t.Fatalf("nextStep should show remedy: %q", out)
	}
}

func TestAttentionNextStepWithOnlyReason(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.items = []fleet.AttentionItem{
		{
			Entity: fleet.EntityRef{Kind: fleet.KindService, Key: "test", Label: "test"},
			Reason: "just a reason",
		},
	}
	s.tbl.SetCursor(0)
	out := s.nextStep()
	if !strings.Contains(out, "just a reason") {
		t.Fatalf("nextStep should show reason: %q", out)
	}
}

func TestAttentionNextStepOnEmptyList(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	s.items = nil
	if out := s.nextStep(); out != "" {
		t.Fatalf("nextStep on empty list returned %q, want empty", out)
	}
}

func TestAttentionResizeHandlesSmallHeights(t *testing.T) {
	c := newLoadedContext(t)
	s := newAttentionScreen(c).(*attentionScreen)
	c.Height = 2
	s.resize(c)
	out := s.View(c)
	if out == "" {
		t.Fatal("View() should render even with small height")
	}
}

func TestAttentionLoadWithInvalidFilterSetsError(t *testing.T) {
	c := newLoadedContext(t)
	a := newAttentionScreen(c).(*attentionScreen)
	a.load(c, fleet.AttentionFilter{Limit: -1})
	if a.loadErr == nil {
		t.Fatal("load with negative limit should produce an error")
	}
	if a.items != nil {
		t.Fatal("items should be cleared after a query error")
	}
	if a.tbl.Rows() != nil {
		t.Fatal("table rows should be cleared after a query error")
	}
	out := a.View(c)
	if !strings.Contains(out, "attention query failed") {
		t.Fatalf("View should surface the query error:\n%s", out)
	}
}
