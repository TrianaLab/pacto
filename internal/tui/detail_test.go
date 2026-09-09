package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestDetailRendersEachKind(t *testing.T) {
	c := newLoadedContext(t)
	list, err := c.Query.Entities(fleet.EntityFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[fleet.EntityKind]bool{}
	for _, ref := range list.Entities {
		if seen[ref.Kind] {
			continue
		}
		seen[ref.Kind] = true
		s := newDetailScreen(c, ref)
		out := s.View(c)
		if out == "" {
			t.Errorf("%s detail rendered nothing", ref.Kind)
		}
		if strings.Contains(out, "failed") {
			t.Errorf("%s detail reported a failure: %s", ref.Kind, out)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("the fixture only produced %d kinds; the test needs at least 2", len(seen))
	}
}

func TestDetailSurfacesALookupError(t *testing.T) {
	c := newLoadedContext(t)
	s := newDetailScreen(c, fleet.EntityRef{Kind: fleet.KindService, Key: "no-such-key"})
	if !strings.Contains(s.View(c), "failed") {
		t.Fatalf("a missing entity must report the failure:\n%s", s.View(c))
	}
}

func TestDetailSelectedIsItsOwnRef(t *testing.T) {
	c := newLoadedContext(t)
	ref := fleet.EntityRef{Kind: fleet.KindService, Key: "k", Label: "l"}
	got, ok := newDetailScreen(c, ref).(*detailScreen).selected()
	if !ok || got.Key != "k" {
		t.Fatalf("selected() = %+v, %v", got, ok)
	}
}

func TestDetailContentIsSetBeforeTheFirstRender(t *testing.T) {
	c := newLoadedContext(t)
	d := newDetailScreen(c, firstEntityOfKind(t, c, fleet.KindService)).(*detailScreen)
	// Nothing has called View or Update yet, so this proves the constructor
	// loaded the viewport rather than a later resize doing it.
	if d.vp.GetContent() == "" {
		t.Fatal("the viewport has no content before the first render")
	}
}

func TestDetailKeepsItsScrollPositionAcrossMessages(t *testing.T) {
	c := newLoadedContext(t)
	c.Height = 8 // force the body to overflow so there is somewhere to scroll
	s := newDetailScreen(c, firstEntityOfKind(t, c, fleet.KindRevision))

	for range 3 {
		s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	scrolled := s.(*detailScreen).vp.YOffset()
	if scrolled == 0 {
		t.Fatal("the revision detail did not scroll; the fixture body is too short to test this")
	}

	// A message the viewport does nothing with must not send the reader back to
	// the top of a long page.
	s, _ = s.Update(c, statusMsg{text: "unrelated"})
	if got := s.(*detailScreen).vp.YOffset(); got != scrolled {
		t.Errorf("scroll moved to %d on an unrelated message, want %d", got, scrolled)
	}
}

func TestEnterOnTheListOpensDetail(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	_, cmd := s.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg, ok := cmd().(pushMsg)
	if !ok {
		t.Fatalf("enter produced %T, want pushMsg", cmd())
	}
	if msg.s.Title() == "" {
		t.Fatal("pushed screen has no title")
	}
}

func TestEnterOnAnEmptyListDoesNothing(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = nil
	l.tbl.SetRows(nil)
	if _, cmd := l.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter on an empty list must not push a screen")
	}
}

func TestEnterWhileTypingAppliesTheFilter(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.typing = true
	l.input.SetValue("xyz")
	_, cmd := l.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("enter while typing must not push a screen")
	}
	if l.filterText != "xyz" {
		t.Fatalf("filterText = %q, want xyz", l.filterText)
	}
	if l.typing {
		t.Fatal("typing still true after enter")
	}
}

func TestEnterOnAttentionOpensDetail(t *testing.T) {
	c := newLoadedContext(t)
	a := newAttentionScreen(c).(*attentionScreen)
	if len(a.items) == 0 {
		t.Fatalf("attention screen is empty")
	}
	_, cmd := a.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg, ok := cmd().(pushMsg)
	if !ok {
		t.Fatalf("enter produced %T, want pushMsg", cmd())
	}
	if msg.s.Title() == "" {
		t.Fatal("pushed screen has no title")
	}
}

func TestEnterOnEmptyAttentionDoesNothing(t *testing.T) {
	c := newLoadedContext(t)
	a := newAttentionScreen(c).(*attentionScreen)
	a.items = nil
	a.tbl.SetRows(nil)
	if _, cmd := a.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter on an empty attention list must not push a screen")
	}
}

func TestDetailRendersEmptyEnvelope(t *testing.T) {
	out := renderDetail(&fleet.EntityDetail{})
	if !strings.Contains(out, "no payload") {
		t.Fatalf("an empty envelope must say so:\n%s", out)
	}
}

func TestFieldSkipsEmptyValues(t *testing.T) {
	var b strings.Builder
	field(&b, "label", "")
	if b.Len() > 0 {
		t.Fatalf("field rendered an empty value: %q", b.String())
	}
}

func TestDetailResizeHandlesSmallHeights(t *testing.T) {
	c := newLoadedContext(t)
	c.Height = 2
	s := newDetailScreen(c, fleet.EntityRef{Kind: fleet.KindService, Key: testServiceName})
	out := s.View(c)
	if out == "" {
		t.Fatal("detail with height 2 rendered nothing")
	}
}

func TestDetailTitleFallsBackToKey(t *testing.T) {
	c := newLoadedContext(t)
	ref := fleet.EntityRef{Kind: fleet.KindService, Key: "the-key"}
	s := newDetailScreen(c, ref).(*detailScreen)
	if s.Title() != "the-key" {
		t.Fatalf("Title() = %q, want the-key", s.Title())
	}
}

func TestDetailUpdateReturnsTheSameScreenNotACopy(t *testing.T) {
	// The screen interface lets Update return a different screen, which is how
	// navigation works. A detail screen must not use that: returning a fresh
	// value here would discard the loaded body and the scroll position on every
	// message.
	c := newLoadedContext(t)
	s := newDetailScreen(c, fleet.EntityRef{Kind: fleet.KindService, Key: testServiceName})
	out, _ := s.Update(c, tea.WindowSizeMsg{Width: 50, Height: 20})
	if out != s {
		t.Fatal("Update must return the receiver")
	}
}

func TestRenderServiceDetailWithAllFields(t *testing.T) {
	var b strings.Builder
	s := &fleet.ServiceDetailData{
		Domain: "example.com",
		Ownership: &fleet.OwnershipInfo{Owner: "team-a"},
		Summary: fleet.ServiceSummary{
			Revisions:      5,
			RevisionsInUse: 3,
			Targets:        10,
			DeclaredDependencies: 2,
		},
		Dependents: fleet.RefPreview{Total: 4},
		ActiveRevisions: fleet.RefPreview{
			Total: 2,
			Items: []fleet.EntityRef{{Label: "rev-1"}, {Label: "rev-2"}},
		},
		Findings: fleet.AttributedFindingsPreview{
			Total: 1,
			Items: []fleet.AttributedFinding{
				{
					Finding: fleet.ProductFinding{Severity: "error", Message: "test error"},
					Entity:  fleet.EntityRef{Label: "target-1"},
				},
			},
		},
	}
	renderServiceDetail(&b, s)
	out := b.String()
	if !strings.Contains(out, "example.com") || !strings.Contains(out, "team-a") {
		t.Fatalf("missing expected content:\n%s", out)
	}
}

func TestRenderRevisionDetailWithAllFields(t *testing.T) {
	var b strings.Builder
	r := &fleet.RevisionDetailData{
		Service:      fleet.EntityRef{Label: "test-svc"},
		Version:      "1.0.0",
		PactoVersion: "2.0",
		Valid:        true,
		Workload:     "deployment",
		Ownership:    &fleet.OwnershipInfo{Owner: "team-b"},
		Identity: fleet.RevisionIdentity{
			IdentityClass: "exact",
			Digest:        "sha256:abc",
			RequestedRef:  "oci://reg/repo:v1",
			ResolvedRef:   "oci://reg/repo@sha256:abc",
			Retrievable:   true,
		},
		Provenance: fleet.RevisionProvenance{Source: "github.com/org/repo"},
		Readiness: &fleet.ProductReadiness{
			Passing:      true,
			Score:        100,
			MinScore:     80,
			DoneCount:    5,
			NotDoneCount: 0,
		},
		Interfaces: fleet.InterfacesPreview{
			Total: 1,
			Items: []fleet.InterfaceSummary{{Name: "http", Type: "openapi"}},
		},
		Configurations: fleet.ConfigurationsPreview{
			Total: 1,
			Items: []fleet.ConfigurationSummary{{Name: "db"}},
		},
		Policies: fleet.PoliciesPreview{
			Total: 1,
			Items: []fleet.PolicySummary{{Name: "security"}},
		},
		ExactTargets:    fleet.RefPreview{Total: 2},
		InferredTargets: fleet.RefPreview{Total: 1},
	}
	renderRevisionDetail(&b, r)
	out := b.String()
	if !strings.Contains(out, "1.0.0") || !strings.Contains(out, "deployment") {
		t.Fatalf("missing expected content:\n%s", out)
	}
}

func TestRenderTargetDetailWithAllFields(t *testing.T) {
	var b strings.Builder
	tgt := &fleet.TargetDetailData{
		Service:    fleet.EntityRef{Label: "svc-1"},
		Revision:   &fleet.EntityRef{Label: "rev-1"},
		LinkState:  "exact",
		Scope:      "prod",
		Kind:       "deployment",
		Compliance: "Compliant",
		Source:     "k8s",
		Stale:      false,
		Ownership:  &fleet.OwnershipInfo{Owner: "team-c"},
		Coverage:   &fleet.Coverage{Evaluated: 5, Required: 5},
		Readiness: &fleet.ProductReadiness{
			Passing:      true,
			Score:        90,
			MinScore:     70,
			DoneCount:    4,
			NotDoneCount: 1,
		},
		ObservedRuntime: fleet.RuntimePreview{
			Count: 2,
			Items: []fleet.RuntimeFact{{Key: "replica", Value: "3"}},
		},
		Findings: fleet.FindingsPreview{
			Total: 1,
			Items: []fleet.ProductFinding{{Severity: "warning", Message: "test warning"}},
		},
	}
	renderTargetDetail(&b, tgt)
	out := b.String()
	if !strings.Contains(out, "svc-1") || !strings.Contains(out, "exact") {
		t.Fatalf("missing expected content:\n%s", out)
	}
}

func TestRenderOwnerDetailWithAllFields(t *testing.T) {
	var b strings.Builder
	o := &fleet.OwnerDetailData{
		Summary: fleet.OwnerSummary{
			Services:  10,
			Revisions: 20,
			Targets:   30,
		},
		Services: fleet.RefPreview{
			Total: 3,
			Items: []fleet.EntityRef{{Label: "svc-a"}, {Label: "svc-b"}},
			Truncated: true,
		},
		Attention: fleet.AttentionPreview{
			Total: 1,
			Items: []fleet.AttentionItem{
				{Severity: "error", Service: "svc-a", Summary: "needs attention"},
			},
		},
	}
	renderOwnerDetail(&b, o)
	out := b.String()
	if !strings.Contains(out, "svc-a") || !strings.Contains(out, "30") {
		t.Fatalf("missing expected content:\n%s", out)
	}
}

func TestRenderSourceDetailWithAllFields(t *testing.T) {
	var b strings.Builder
	ts := "2024-01-01T00:00:00Z"
	lastSync, _ := time.Parse(time.RFC3339, ts)
	observedAt, _ := time.Parse(time.RFC3339, ts)
	src := &fleet.SourceDetailData{
		Kind:               "oci",
		Health:             "available",
		LastSuccessfulSync: &lastSync,
		ObservedAt:         &observedAt,
		RevisionCount:      5,
		TargetCount:        10,
		Contributed: fleet.SourceContribution{
			Services:  3,
			Revisions: 5,
			Targets:   10,
		},
		Error: &fleet.SourceError{Message: "transient error"},
	}
	renderSourceDetail(&b, src)
	out := b.String()
	if !strings.Contains(out, "oci") || !strings.Contains(out, "available") {
		t.Fatalf("missing expected content:\n%s", out)
	}
}

func TestRenderDetailForEachKind(t *testing.T) {
	tests := []struct {
		name string
		d    *fleet.EntityDetail
		want string
	}{
		{
			"service",
			&fleet.EntityDetail{Service: &fleet.ServiceDetailData{Domain: "test.com"}},
			"test.com",
		},
		{
			"revision",
			&fleet.EntityDetail{Revision: &fleet.RevisionDetailData{Version: "1.0.0"}},
			"1.0.0",
		},
		{
			"target",
			&fleet.EntityDetail{Target: &fleet.TargetDetailData{LinkState: "exact"}},
			"exact",
		},
		{
			"owner",
			&fleet.EntityDetail{Owner: &fleet.OwnerDetailData{Summary: fleet.OwnerSummary{Services: 5}}},
			"Summary",
		},
		{
			"source",
			&fleet.EntityDetail{Source: &fleet.SourceDetailData{Kind: "local"}},
			"local",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderDetail(tt.d)
			if !strings.Contains(out, tt.want) {
				t.Fatalf("missing %q in output:\n%s", tt.want, out)
			}
		})
	}
}

func TestRenderDetailWithTruncatedPreviews(t *testing.T) {
	d := &fleet.EntityDetail{
		Service: &fleet.ServiceDetailData{
			ActiveRevisions: fleet.RefPreview{
				Total:     10,
				Items:     []fleet.EntityRef{{Label: "r1"}},
				Truncated: true,
			},
			Findings: fleet.AttributedFindingsPreview{
				Total: 20,
				Items: []fleet.AttributedFinding{
					{Finding: fleet.ProductFinding{Severity: "error", Message: "err"}, Entity: fleet.EntityRef{Label: "t1"}},
				},
				Truncated: true,
			},
		},
	}
	out := renderDetail(d)
	if !strings.Contains(out, "and 9 more") || !strings.Contains(out, "and 19 more") {
		t.Fatalf("missing truncation indicators:\n%s", out)
	}
}

func TestRenderTargetWithTruncatedRuntime(t *testing.T) {
	var b strings.Builder
	total := 100
	t2 := &fleet.TargetDetailData{
		Service:   fleet.EntityRef{Label: "s"},
		LinkState: "exact",
		ObservedRuntime: fleet.RuntimePreview{
			Total:     &total,
			Count:     5,
			Items:     []fleet.RuntimeFact{{Key: "k", Value: "v"}},
			Truncated: true,
		},
		Findings: fleet.FindingsPreview{
			Total:     10,
			Items:     []fleet.ProductFinding{{Severity: "info", Message: "msg"}},
			Truncated: true,
		},
	}
	renderTargetDetail(&b, t2)
	out := b.String()
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "and 9 more") {
		t.Fatalf("missing truncation indicators:\n%s", out)
	}
}

func TestRenderOwnerWithTruncatedServices(t *testing.T) {
	var b strings.Builder
	o := &fleet.OwnerDetailData{
		Summary: fleet.OwnerSummary{Services: 100},
		Services: fleet.RefPreview{
			Total:     100,
			Items:     []fleet.EntityRef{{Label: "s1"}},
			Truncated: true,
		},
		Attention: fleet.AttentionPreview{
			Total:     50,
			Items:     []fleet.AttentionItem{{Severity: "warning", Service: "s1", Summary: "warn"}},
			Truncated: true,
		},
	}
	renderOwnerDetail(&b, o)
	out := b.String()
	if !strings.Contains(out, "and 99 more") || !strings.Contains(out, "and 49 more") {
		t.Fatalf("missing truncation indicators:\n%s", out)
	}
}

func TestDetailPressGPushesGraphScreen(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newDetailScreen(c, ref)
	_, cmd := s.Update(c, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if cmd == nil {
		t.Fatal("g did not produce a command")
	}
	msg := cmd()
	pm, ok := msg.(pushMsg)
	if !ok {
		t.Fatalf("g produced %T, want pushMsg", msg)
	}
	if !strings.Contains(pm.s.Title(), "Graph:") {
		t.Fatalf("g pushed %q, want a graph screen", pm.s.Title())
	}
}

func TestDetailSelectedAlwaysReturnsTrue(t *testing.T) {
	// Detail screen always has a selection (the ref it was opened with)
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	d := newDetailScreen(c, ref).(*detailScreen)
	_, ok := d.selected()
	if !ok {
		t.Fatal("detail screen's selected() should always return true")
	}
}
