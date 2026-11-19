package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// TestDetailTitlePrefersTheLabel is the other side of the fallback. A revision
// key carries a digest, so a title taken from the key is a header of hex where
// the reader expects a name -- and only a ref with both fields set can tell the
// preference from the fallback.
func TestDetailTitlePrefersTheLabel(t *testing.T) {
	c := newLoadedContext(t)
	ref := fleet.EntityRef{Kind: fleet.KindService, Key: "the-key", Label: "the-label"}
	s := newDetailScreen(c, ref).(*detailScreen)
	if got := s.Title(); got != "the-label" {
		t.Fatalf("Title() = %q, want the-label", got)
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
		Domain:    "example.com",
		Ownership: &fleet.OwnershipInfo{Owner: "team-a"},
		Summary: fleet.ServiceSummary{
			Revisions:            5,
			RevisionsInUse:       3,
			Targets:              10,
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
	renderServiceDetail(&b, "web", s, fleet.CompletenessComplete)
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
			Total:     3,
			Items:     []fleet.EntityRef{{Label: "svc-a"}, {Label: "svc-b"}},
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
	got, ok := d.selected()
	if !ok {
		t.Fatal("detail screen's selected() should always return true")
	}
	if got.Key != ref.Key {
		t.Fatalf("selected() = %+v, want %+v", got, ref)
	}

	// "Always" is the claim in the name, so the ref the screen can say least
	// about has to hold it too: the verb layer decides what applies from the
	// selection's own fields, and a screen that reported "nothing selected"
	// here would silently offer no verbs at all rather than saying why.
	zero := newDetailScreen(c, fleet.EntityRef{}).(*detailScreen)
	if _, ok := zero.selected(); !ok {
		t.Fatal("a detail screen over a zero ref reported no selection")
	}
}

func TestRenderDetailForADomain(t *testing.T) {
	out := renderDetail(&fleet.EntityDetail{
		Entity: fleet.EntityRef{Kind: fleet.KindDomain, Key: "payments"},
		Domain: &fleet.DomainDetailData{
			Summary:   fleet.OwnerSummary{Services: 2},
			Ownership: fleet.OwnershipTally{Unowned: 1},
			Owners:    fleet.RefPreview{Total: 1},
			Services:  fleet.RefPreview{Total: 1, Count: 1, Items: []fleet.EntityRef{{Label: "pay"}}},
		},
	})
	for _, want := range []string{"owners", "no owner", "services", "pay"} {
		if !strings.Contains(out, want) {
			t.Errorf("domain detail never says %q:\n%s", want, out)
		}
	}
	svc := renderDetail(&fleet.EntityDetail{Entity: fleet.EntityRef{BusinessDomain: "payments", Offering: "postgres"}, Service: &fleet.ServiceDetailData{Domain: "ghcr.io/acme", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}}})
	if !strings.Contains(svc, "domain") || !strings.Contains(svc, "payments") || !strings.Contains(svc, "registry") ||
		!strings.Contains(svc, "offering") || !strings.Contains(svc, "postgres") || !strings.Contains(svc, "deprecated 2026-01-01") {
		t.Errorf("service detail = %s", svc)
	}
}

func TestBusinessDomainTextSaysUnknown(t *testing.T) {
	svc := renderDetail(&fleet.EntityDetail{Entity: fleet.EntityRef{BusinessDomainUnknown: true}, Service: &fleet.ServiceDetailData{}})
	if !strings.Contains(svc, "unknown") {
		t.Errorf("service detail with an unknown domain = %s", svc)
	}
	if got := businessDomainText(fleet.EntityRef{BusinessDomain: "pay"}); got != "pay" {
		t.Errorf("businessDomainText = %q", got)
	}
}

func TestRenderDetailForAnOffering(t *testing.T) {
	out := renderDetail(&fleet.EntityDetail{
		Entity: fleet.EntityRef{Kind: fleet.KindOffering, Key: "postgres"},
		Offering: &fleet.OfferingDetailData{
			Providers: fleet.OfferingProvidersPreview{Total: 2, Count: 1, Truncated: true, Items: []fleet.OfferingProvider{{
				Service:   fleet.EntityRef{Label: "pg-operator"},
				Ownership: &fleet.OwnershipInfo{Owner: "team-data"},
				Versions: fleet.ProviderVersionsPreview{Total: 4, Count: 3, Truncated: true, Items: []fleet.ProviderVersion{
					{Revision: fleet.EntityRef{Label: "1.2.0"}},
					{Revision: fleet.EntityRef{Label: "1.1.0"}, Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}},
					{Revision: fleet.EntityRef{Label: "1.0.0"}, Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2025-01-01"}, SunsetReached: true},
				}},
				Consume:   &fleet.ConsumeSnippet{Ref: "oci://ghcr.io/acme/pg-operator", YAML: "dependencies:\n  - name: pg-operator\n"},
				Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2027-06-30"},
			}, {Service: fleet.EntityRef{Label: "bare"}}}},
			Consumers: fleet.OfferingUsesPreview{Total: 2, Count: 1, Truncated: true, Items: []fleet.OfferingUse{{
				Consumer: fleet.EntityRef{Label: "orders"}, Provider: fleet.EntityRef{Label: "pg-operator"},
			}}},
		},
	})
	for _, want := range []string{"Providers (2)", "pg-operator", "team-data", "1.2.0, 1.1.0 (deprecated), 1.0.0 (sunset), +1 more", "deprecated 2026-01-01, sunset 2027-06-30", "oci://ghcr.io/acme/pg-operator", "    dependencies:\n", "      - name: pg-operator\n", "bare", "Consumers (2)", "orders -> pg-operator", "and 1 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("offering detail never says %q:\n%s", want, out)
		}
	}
}

func TestRenderServiceDetail_GoldenPath(t *testing.T) {
	var b strings.Builder
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Template: &fleet.TemplateDetail{
		Plugins:               []contract.TemplatePlugin{{Name: "dockerfile", Description: "A Dockerfile"}},
		Requirements:          fleet.OfferingUsesPreview{Total: 2},
		CreatedServices:       fleet.RefPreview{Total: 1},
		HiddenCreatedServices: 2,
		HiddenRequirements:    1,
		Signature:             &fleet.BundleSignature{Status: fleet.SignatureUnsigned},
		Instantiate:           &fleet.TemplateInstantiation{CLI: "pacto init <name> --from oci://r/p@sha256:x --owner <team>"},
	}}, fleet.CompletenessComplete)
	out := b.String()
	for _, want := range []string{"Golden path", "unsigned", "dockerfile: A Dockerfile", "3 (0 unresolved)", "3", "pacto init <name> --from oci://r/p@sha256:x"} {
		if !strings.Contains(out, want) {
			t.Errorf("golden path detail lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Template: &fleet.TemplateDetail{InstantiateReason: "no digest-pinned reference"}}, fleet.CompletenessComplete)
	if !strings.Contains(b.String(), "no digest-pinned reference") || !strings.Contains(b.String(), "not-checked") {
		t.Errorf("a golden path with no pinned reference gives the reason:\n%s", b.String())
	}
}

func TestRenderServiceDetail_Origin(t *testing.T) {
	var b strings.Builder
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{
		Origin: &fleet.OriginDetail{Ref: "oci://r/http-service:1.0.0", Digest: "sha256:abc", GoldenPath: &fleet.EntityRef{Label: "http-service"}},
		Creation: []fleet.CreationStep{
			{Step: fleet.StepContract, Status: fleet.StepStatusDone, Detail: "read"},
			{Step: fleet.StepPlugin, Name: "dockerfile", Status: fleet.StepStatusUnknown, Detail: "not observed"},
		},
	}, fleet.CompletenessComplete)
	out := b.String()
	for _, want := range []string{"Created from (declared)", "oci://r/http-service:1.0.0", "sha256:abc", "http-service", "done: read", "plugin dockerfile", "unknown: not observed"} {
		if !strings.Contains(out, want) {
			t.Errorf("origin detail lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Origin: &fleet.OriginDetail{Ref: "oci://r/p:1", Reason: "the golden path it names is not in this view"}}, fleet.CompletenessComplete)
	if !strings.Contains(b.String(), "not in this view") {
		t.Errorf("an unresolved origin gives the reason:\n%s", b.String())
	}
}

func TestRenderServiceEnvironments(t *testing.T) {
	var b strings.Builder
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Placement: fleet.PlacementPreview{Total: 2, Count: 1, Truncated: true, Items: []fleet.EnvironmentPlacement{
		{Environment: "prod\x1b[31m", Targets: 1, Declared: 1, RevisionsTotal: 1, Revisions: []fleet.EntityRef{{Version: "1.0.0"}},
			Declaration: &fleet.EnvironmentResult{Status: fleet.EnvironmentPass, Lifetime: fleet.LifetimeNotDeclared},
			Owner:       fleet.EnvironmentOwner{Owner: "team-a", Provenance: "declared"}, TargetsComplete: true,
			ProvisioningReason: "no resource use of the current revision provisions a resource"},
	}, DifferencesError: "environment b: cannot read b.yaml"}}, fleet.CompletenessComplete)
	out := b.String()
	for _, want := range []string{"Environments", "prod", "declared: values pass, lifetime not declared; owner team-a (declared); targets you can see: 1 (1 declared), 1.0.0",
		"provisioning not tracked", "1 more environments", "Differences unknown: environment b: cannot read b.yaml", "Stages: pacto delivery status --service web"} {
		if !strings.Contains(out, want) {
			t.Errorf("environments lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[31m") {
		t.Errorf("environment names go through safeText:\n%q", out)
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Placement: fleet.PlacementPreview{Total: 1, Count: 1, Items: []fleet.EnvironmentPlacement{{Targets: 1}}}}, fleet.CompletenessPartial)
	if !strings.Contains(b.String(), "Environments (targets Pacto can see)") || !strings.Contains(b.String(), "targets you can see: 1 (incomplete)") {
		t.Errorf("a partial answer says the targets are those Pacto can see:\n%s", b.String())
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{}, fleet.CompletenessComplete)
	if strings.Contains(b.String(), "Environments") {
		t.Errorf("no environments, no section:\n%s", b.String())
	}
}

func TestRenderServiceChanges(t *testing.T) {
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Label: "svc 1.1.0\x1b[31m"}
	var b strings.Builder
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Changes: fleet.ChangeHistoryPreview{Total: 2, Count: 2, Gaps: 1, Items: []fleet.ChangeEvent{
		{Kind: "environment", Subject: "prod", Change: "changed", Fields: []string{"owner"}, Revision: rev},
		{Kind: "owner", Change: "present", To: "team:web", Revision: rev},
	}}}, fleet.CompletenessComplete)
	out := b.String()
	for _, want := range []string{"Changes, version order (2 of 2, 1 revision(s) unread)", "environment prod changed (owner)  time not read", "owner team:web declared since at least svc 1.1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("changes lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[31m") || strings.Count(out, "time not read") != 2 {
		t.Errorf("change lines:\n%q", out)
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{}, fleet.CompletenessComplete)
	if strings.Contains(b.String(), "Changes") {
		t.Errorf("no changes, no section:\n%s", b.String())
	}
}

// TestRenderDetailForAnOfferingAvailability: the offering page shows its
// availability per environment, each provider's own, the successor and the
// newest supported revision, each use's successor range and the callers seen
// without a declaration, hidden ones only counted.
func TestRenderDetailForAnOfferingAvailability(t *testing.T) {
	prod := fleet.EnvironmentAvailability{Environment: "prod", State: fleet.AvailabilityDegraded, Provenance: fleet.ProvenanceInferred, Reason: "1 of 2 consumers cannot reach it", Partial: true}
	avail := fleet.Availability{State: fleet.AvailabilityDegraded, ByEnvironment: []fleet.EnvironmentAvailability{prod}, Total: 1}
	rev := fleet.EntityRef{Label: "pg 17.0.0"}
	out := renderDetail(&fleet.EntityDetail{
		Entity: fleet.EntityRef{Kind: fleet.KindOffering, Key: "pg"},
		Offering: &fleet.OfferingDetailData{
			Availability: avail, HiddenProviders: 1,
			Providers: fleet.OfferingProvidersPreview{Total: 1, Count: 1, Items: []fleet.OfferingProvider{{
				Service: fleet.EntityRef{Label: "pg"}, Availability: avail,
				Successor: &fleet.SuccessorInfo{Ref: "oci://x/pg:17.0.0", Revision: &rev}, LatestSupported: &fleet.EntityRef{Label: "pg 16.0.1"},
			}}},
			Consumers: fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{{
				Consumer: fleet.EntityRef{Label: "orders"}, Provider: fleet.EntityRef{Label: "pg"}, SuccessorRange: fleet.RangeUnsatisfied, SupportedRange: fleet.RangeUnknown,
			}}},
			ObservedConsumers:       fleet.ObservedConsumersPreview{Total: 1, Count: 1, Items: []fleet.ObservedConsumer{{Caller: "10.0.0.1", Uncontracted: true}}},
			HiddenObservedConsumers: 2,
		},
	})
	for _, want := range []string{
		"Availability", "degraded", "  prod: degraded (inferred, 1 of 2 consumers cannot reach it) [partial]", "1 you may not see",
		"    prod: degraded (inferred", "oci://x/pg:17.0.0 (pg 17.0.0)", "pg 16.0.1", "orders -> pg, successor range unsatisfied, latest supported range unknown",
		"Also calling a provider (observed, no declaration): 10.0.0.1 (no contract, last seen unknown)", "Observed callers you may not see: 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("offering detail never says %q:\n%s", want, out)
		}
	}
}

func TestRenderServiceResources(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	var b strings.Builder
	renderServiceDetail(&b, "notifier", &fleet.ServiceDetailData{HiddenProviders: 2, ConsumedOfferings: fleet.OfferingUsesPreview{Total: 3, Count: 1, Truncated: true, Items: []fleet.OfferingUse{{
		Provider: fleet.EntityRef{Label: "postgresql\x1b[31m"}, Offering: "postgresql", Via: fleet.RelationshipConfigRef,
		ProviderOwner: &fleet.OwnershipInfo{Owner: "infra-data"},
		Requested:     []fleet.RequestedConfiguration{{Provenance: "declared", Configuration: "db", Values: fleet.RuntimePreview{Items: []fleet.RuntimeFact{{Key: "size", Value: "medium"}}}}},
		Provisioning:  []fleet.ProvisioningStatus{{Environment: "production", Phase: "Ready", Provider: "aws", Source: "evidence", Verified: true, AsOf: &at}},
		Consumption:   []fleet.ResourceConsumption{{State: "unknown", Reason: "the usage report is not signed", Reported: &fleet.UsageFigures{}, Source: "target-state"}},
	}}}}, fleet.CompletenessComplete)
	out := b.String()
	for _, want := range []string{"Resources (3)", " (offering postgresql) via configuration_reference", "infra-data",
		"requested (declared): size=medium, configuration db", "production  Ready  aws  signed  asOf 2026-11-01T09:00:00Z",
		"usage: unknown: the usage report is not signed", "... and 2 more", "at least 2 resources from providers you may not see", fleet.ResourceActionsNote} {
		if !strings.Contains(out, want) {
			t.Errorf("resources lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[31m") {
		t.Errorf("provider labels go through safeText:\n%q", out)
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{}, fleet.CompletenessComplete)
	if strings.Contains(b.String(), "Resources") {
		t.Errorf("no resources, no section:\n%s", b.String())
	}
	b.Reset()
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{ConsumedOfferings: fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{{Provider: fleet.EntityRef{Label: "email-provider"}, Via: fleet.RelationshipDependency, RequestedTruncated: true}}}}, fleet.CompletenessComplete)
	// A cut request list and an unknown owner say so, as the CLI prints them.
	for _, want := range []string{"email-provider (no offering) via dependency", "owner unknown, availability unknown", "requested values may be missing: the configurations list was cut"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("resources lack %q:\n%s", want, b.String())
		}
	}
}

// An offering page names its adapters through safeText, says Unknown when
// none reported and shows the unresolved uses.
func TestRenderDetailForAnOfferingAdapters(t *testing.T) {
	render := func(o *fleet.OfferingDetailData) string {
		return renderDetail(&fleet.EntityDetail{Entity: fleet.EntityRef{Kind: fleet.KindOffering, Key: "scan"}, Offering: o})
	}
	out := render(&fleet.OfferingDetailData{
		Adapters: fleet.OfferingAdaptersPreview{Total: 2, Count: 2, Unattributed: 1, Items: []fleet.OfferingAdapter{
			{Provider: "scanner", Producer: "security-adapter", Verified: true, Environments: []string{"production"}, Phases: fleet.PhaseTally{Ready: 1}},
			{Producer: "platform-adapter", Environments: []string{"default"}, Phases: fleet.PhaseTally{Unknown: 1}},
		}},
		UnresolvedUses: fleet.UnresolvedUses{Count: 1, Reason: "not pinned"},
	})
	for _, want := range []string{"Adapters (2)", "scanner by security-adapter, signed: production; 1 ready", "unnamed adapter by platform-adapter", "1 requests no adapter reports on", "1 may name a provider of this offering (inferred): not pinned"} {
		if !strings.Contains(out, want) {
			t.Errorf("offering detail never says %q:\n%s", want, out)
		}
	}
	if out := render(&fleet.OfferingDetailData{}); !strings.Contains(out, "No adapter has reported. Provisioning status is Unknown.") || strings.Contains(out, "unresolved uses") {
		t.Errorf("empty adapters:\n%s", out)
	}
	if out := render(&fleet.OfferingDetailData{HiddenConsumers: 1}); !strings.Contains(out, fleet.NoAdapterVisible) {
		t.Errorf("empty adapters with hidden consumers:\n%s", out)
	}
	out = render(&fleet.OfferingDetailData{Adapters: fleet.OfferingAdaptersPreview{Total: 1, Count: 1, Items: []fleet.OfferingAdapter{{Provider: "evil\x1b[31m", Environments: []string{"default"}}}}})
	if !strings.Contains(out, "evil^[[31m") {
		t.Errorf("an adapter slug with an escape is not neutralized:\n%q", out)
	}
}

func TestRevisionDetailRenderMigrated(t *testing.T) {
	var b strings.Builder
	renderRevisionDetail(&b, &fleet.RevisionDetailData{PactoVersion: "2.5", MigratedFrom: "2.4"})
	if !strings.Contains(b.String(), "2.5 (migrated from 2.4, inferred)") {
		t.Errorf("revision detail misses the migrated version:\n%s", b.String())
	}
}

func TestRevisionDetailShowsBehindVersion(t *testing.T) {
	var b strings.Builder
	renderRevisionDetail(&b, &fleet.RevisionDetailData{PactoVersion: "2.3", PactoVersionCurrent: "2.5"})
	if !strings.Contains(b.String(), "2.3 (behind 2.5)") {
		t.Errorf("revision detail misses the behind version:\n%s", b.String())
	}
}

// TestTUIExternalDetail: an external system's page carries the CLI's three
// blocks: its class and consumers, its sources and its catalog.
func TestTUIExternalDetail(t *testing.T) {
	rec := fleet.ExternalRecord{
		Key: "stripe", Class: "vendor-api", ConsumersTotal: 2, SourcesTotal: 2, HiddenConsumers: 1,
		Sources: []fleet.ExternalSource{{Service: "checkout", Index: 0, Dependency: "Stripe", Provenance: "declared"},
			{Service: "payments", Index: 1, Dependency: "stripe", Provenance: "declared", Superseded: true}},
		Catalog: &fleet.ExternalCatalogMatch{Origin: "default", Digest: "sha256:abc", Entry: "stripe"},
	}
	out := renderDetail(&fleet.EntityDetail{External: &fleet.ExternalDetailData{External: rec, FoldedInto: fleet.OtherEgress}})
	for _, want := range []string{"vendor-api", "1 you may not see", "other-egress", "Sources (2)", "checkout pacto.yaml dependencies[0] (name: Stripe, declared)",
		"payments pacto.yaml dependencies[1] (name: stripe, declared, not in the newest revision)", "Catalog", "sha256:abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("external detail lacks %q:\n%s", want, out)
		}
	}
	rec.Catalog = nil
	if out := renderDetail(&fleet.EntityDetail{External: &fleet.ExternalDetailData{External: rec}}); !strings.Contains(out, "none (joined by name)") {
		t.Errorf("a name-only external:\n%s", out)
	}
	rec.JoinedBy = "identity"
	if out := renderDetail(&fleet.EntityDetail{External: &fleet.ExternalDetailData{External: rec}}); !strings.Contains(out, "none (joined by its declared identity)") {
		t.Errorf("an identity-joined external reads as joined by name:\n%s", out)
	}
	rec.JoinedBy = ""
	rec.Folded = 3
	if out := renderDetail(&fleet.EntityDetail{External: &fleet.ExternalDetailData{External: rec}}); !strings.Contains(out, "3 external systems") {
		t.Errorf("the other-egress node:\n%s", out)
	}
}

func TestServiceDetailOwnerProvenance(t *testing.T) {
	for prov, want := range map[string]string{
		fleet.OwnerProvenanceDeclaredViaTarget: "pay (declared via target)",
		fleet.OwnerProvenanceAttested:          "pay (attested)",
		fleet.OwnerProvenanceInferred:          "pay (inferred)",
	} {
		var b strings.Builder
		renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Ownership: &fleet.OwnershipInfo{Owner: "pay", Provenance: prov}}, fleet.CompletenessComplete)
		if !strings.Contains(b.String(), want) {
			t.Errorf("%q: want %q in:\n%s", prov, want, b.String())
		}
	}
	var b strings.Builder
	renderServiceDetail(&b, "web", &fleet.ServiceDetailData{Ownership: &fleet.OwnershipInfo{Owner: "pay", Provenance: fleet.OwnerProvenanceDeclared}}, fleet.CompletenessComplete)
	if strings.Contains(b.String(), "(declared") {
		t.Errorf("a declared owner is marked:\n%s", b.String())
	}
}
