package tui

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/diff"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

func TestRenderValidateNil(t *testing.T) {
	got := renderValidate(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil validate result should render as 'no result'")
	}
}

func TestRenderValidateInvalid(t *testing.T) {
	r := &app.ValidateResult{
		Path:  "test.yaml",
		Valid: false,
		Errors: []contract.ValidationError{
			{Code: "TEST_ERROR", Message: "something is wrong"},
		},
	}
	got := renderValidate(r)
	if !strings.Contains(got, "invalid") {
		t.Fatal("invalid result should contain the word 'invalid'")
	}
	if !strings.Contains(got, "TEST_ERROR") {
		t.Fatal("render should include error code")
	}
	if !strings.Contains(got, "something is wrong") {
		t.Fatal("render should include error message")
	}
}

func TestRenderValidateValid(t *testing.T) {
	r := &app.ValidateResult{
		Path:  "test.yaml",
		Valid: true,
	}
	got := renderValidate(r)
	if !strings.Contains(got, "valid") {
		t.Fatal("valid result should contain the word 'valid'")
	}
}

func TestRenderDiffNil(t *testing.T) {
	got := renderDiff(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil diff result should render as 'no result'")
	}
}

func TestRenderDiffBreaking(t *testing.T) {
	r := &app.DiffResult{
		OldPath:        "old.yaml",
		NewPath:        "new.yaml",
		Classification: "BREAKING",
		Changes: []diff.Change{
			{Path: "field.one", Type: diff.Removed, Reason: "test", Classification: diff.Breaking},
		},
	}
	got := renderDiff(r)
	if !strings.Contains(got, "BREAKING") {
		t.Fatal("breaking diff should lead with classification")
	}
}

func TestRenderExplainNil(t *testing.T) {
	got := renderExplain(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil explain result should render as 'no result'")
	}
}

func TestRenderFleetExplainNil(t *testing.T) {
	got := renderFleetExplain(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil fleet explain result should render as 'no result'")
	}
}

func TestRenderFleetExplainWithReasons(t *testing.T) {
	r := &fleet.ExplainResult{
		Kind:    "service",
		Subject: "test-svc",
		Status:  "NonCompliant",
		Reasons: []fleet.Reason{
			{Code: "TEST_CODE", Message: "test reason"},
		},
	}
	got := renderFleetExplain(r)
	if !strings.Contains(got, "TEST_CODE") {
		t.Fatal("render should include reason code")
	}
	if !strings.Contains(got, "test reason") {
		t.Fatal("render should include reason message")
	}
}

func TestRenderLockNil(t *testing.T) {
	got := renderLock(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil lock result should render as 'no result'")
	}
}

func TestRenderLockUpToDate(t *testing.T) {
	r := &app.LockResult{
		Path:         "pacto.lock",
		UpToDate:     true,
		Dependencies: 5,
		References:   3,
	}
	got := renderLock(r)
	if !strings.Contains(got, "up to date") {
		t.Fatal("up-to-date lock should say so")
	}
}

func TestRenderLockWritten(t *testing.T) {
	r := &app.LockResult{
		Path:         "pacto.lock",
		Written:      true,
		Dependencies: 5,
		References:   3,
	}
	got := renderLock(r)
	if !strings.Contains(got, "written") {
		t.Fatal("written lock should say so")
	}
}

func TestRenderImpactNil(t *testing.T) {
	got := renderImpact(nil)
	if !strings.Contains(got, "no result") {
		t.Fatal("nil impact result should render as 'no result'")
	}
}

func TestRenderImpactBreakingWithIncompatibleActiveConsumer(t *testing.T) {
	r := &impact.Result{
		Service:        "test-svc",
		OldVersion:     "1.0.0",
		NewVersion:     "2.0.0",
		Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{
			{
				Service:              "consumer-svc",
				CompatibilityVerdict: impact.CompatibilityIncompatible,
				Targets:              []string{"default/Deployment/consumer"},
			},
		},
	}
	got := renderImpact(r)
	if !strings.Contains(got, "this would fail `pacto impact` in CI") {
		t.Fatal("breaking impact with incompatible active consumer should warn about CI failure")
	}
}

func TestRenderImpactBreakingWithTargetlessIncompatibleConsumer(t *testing.T) {
	r := &impact.Result{
		Service:        "test-svc",
		OldVersion:     "1.0.0",
		NewVersion:     "2.0.0",
		Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{
			{
				Service:              "consumer-svc",
				CompatibilityVerdict: impact.CompatibilityIncompatible,
				Targets:              []string{},
			},
		},
	}
	got := renderImpact(r)
	if strings.Contains(got, "this would fail `pacto impact` in CI") {
		t.Fatal("breaking impact with targetless incompatible consumer should NOT warn about CI failure")
	}
}

func TestRenderExplainWithState(t *testing.T) {
	r := &app.ExplainResult{
		Name:    "test",
		Version: "1.0.0",
		State: &app.ExplainState{
			Type:            "stateful",
			Scope:           "cluster",
			Durability:      "durable",
			DataCriticality: "critical",
		},
	}
	got := renderExplain(r)
	if !strings.Contains(got, "stateful") {
		t.Fatal("render should include state type")
	}
}

func TestRenderExplainWithReadiness(t *testing.T) {
	r := &app.ExplainResult{
		Name:    "test",
		Version: "1.0.0",
		Readiness: &app.ExplainReadiness{
			Score:        90,
			MinScore:     100,
			Passing:      false,
			DoneCount:    5,
			PartialCount: 1,
			NotDoneCount: 2,
		},
	}
	got := renderExplain(r)
	if !strings.Contains(got, "90/100") {
		t.Fatal("render should include readiness score")
	}
}

func TestRenderDiffWithNoChanges(t *testing.T) {
	r := &app.DiffResult{
		OldPath:        "old.yaml",
		NewPath:        "new.yaml",
		Classification: "NON_BREAKING",
		Changes:        []diff.Change{},
	}
	got := renderDiff(r)
	if !strings.Contains(got, "No changes detected") {
		t.Fatal("render should say no changes detected")
	}
}

func TestRenderImpactWithNoConsumers(t *testing.T) {
	r := &impact.Result{
		Service:        "test-svc",
		OldVersion:     "1.0.0",
		NewVersion:     "2.0.0",
		Classification: "NON_BREAKING",
		Consumers:      []impact.AffectedConsumer{},
	}
	got := renderImpact(r)
	if !strings.Contains(got, "none identified") {
		t.Fatal("render should say no consumers identified")
	}
}

func TestRenderImpactWithOwners(t *testing.T) {
	r := &impact.Result{
		Service:        "test-svc",
		OldVersion:     "1.0.0",
		NewVersion:     "2.0.0",
		Classification: "BREAKING",
		Owners:         []string{"team-a", "team-b"},
	}
	got := renderImpact(r)
	if !strings.Contains(got, "team-a") || !strings.Contains(got, "team-b") {
		t.Fatal("render should include owners")
	}
}

func TestRenderDiffWithDependencyChanges(t *testing.T) {
	r := &app.DiffResult{
		OldPath:        "old.yaml",
		NewPath:        "new.yaml",
		Classification: "NON_BREAKING",
		Changes: []diff.Change{
			{Path: "field", Type: diff.Removed, Reason: "removed", Classification: diff.Breaking},
		},
		DependencyDiffs: []app.DependencyDiff{
			{Name: "dep-svc", Classification: "BREAKING", Changes: []diff.Change{{Path: "dep.field", Type: diff.Removed, Reason: "removed", Classification: diff.Breaking}}},
		},
	}
	got := renderDiff(r)
	if !strings.Contains(got, "dep-svc") {
		t.Fatal("render should include dependency changes")
	}
}

func TestRenderExplainWithCapabilitiesInterfacesDependencies(t *testing.T) {
	r := &app.ExplainResult{
		Name:    "test",
		Version: "1.0.0",
		Capabilities: []app.ExplainCapability{
			{Type: "database", Ref: "postgres"},
			{Type: "cache"},
		},
		Interfaces: []app.ExplainInterface{
			{Name: "api", Type: "grpc", Ref: "api.proto"},
		},
		Dependencies: []app.ExplainDependency{
			{Name: "auth-svc", Ref: "oci://example.com/auth:1.0", Required: true, Compatibility: "^1.0"},
		},
	}
	got := renderExplain(r)
	if !strings.Contains(got, "database") {
		t.Fatal("render should include capabilities")
	}
	if !strings.Contains(got, "api") {
		t.Fatal("render should include interfaces")
	}
	if !strings.Contains(got, "auth-svc") {
		t.Fatal("render should include dependencies")
	}
	if !strings.Contains(got, "[required]") {
		t.Fatal("render should mark required dependencies")
	}
}

func TestRenderDiffWithGraphDiff(t *testing.T) {
	r := &app.DiffResult{
		OldPath:        "old.yaml",
		NewPath:        "new.yaml",
		Classification: "NON_BREAKING",
		Changes: []diff.Change{
			{Path: "field", Type: diff.Added, Reason: "added", Classification: diff.NonBreaking},
		},
		GraphDiff: &graph.GraphDiff{Root: graph.DiffNode{Name: "test-svc"}},
	}
	got := renderDiff(r)
	if !strings.Contains(got, "Graph changes") {
		t.Fatalf("render should include graph changes section, got:\n%s", got)
	}
}

// TestRenderersEscapeHostileContractText covers the output pane, which the
// reader consults to decide whether to run a write: a forged diff or a forged
// impact steers that decision at one remove. Every renderer here is fed the
// same payload and has to show it rather than let the frame act on it.
func TestRenderersEscapeHostileContractText(t *testing.T) {
	const hostile = "svc\r\x1b[2Kpacto lock --check ./svc"
	const escaped = "svc^M^[[2Kpacto lock --check ./svc"

	for _, tt := range []struct{ name, got string }{
		{"validate path and message", renderValidate(&app.ValidateResult{
			Path:   hostile,
			Errors: []contract.ValidationError{{Code: hostile, Message: hostile}},
			Warnings: []contract.ValidationWarning{
				{Code: hostile, Message: hostile},
			},
		})},
		{"diff paths, classification and reasons", renderDiff(&app.DiffResult{
			OldPath:        hostile,
			NewPath:        hostile,
			Classification: hostile,
			Changes:        []diff.Change{{Path: hostile, Type: diff.Removed, Reason: hostile}},
			DependencyDiffs: []app.DependencyDiff{
				{Name: hostile, Classification: hostile},
			},
		})},
		{"explain capabilities, interfaces and dependencies", renderExplain(&app.ExplainResult{
			Name:         hostile,
			Version:      "1.0.0",
			Capabilities: []app.ExplainCapability{{Type: hostile, Ref: hostile}, {Type: hostile}},
			Interfaces:   []app.ExplainInterface{{Name: hostile, Type: hostile}},
			Dependencies: []app.ExplainDependency{{Name: hostile, Ref: hostile}},
		})},
		{"fleet explain subject and reasons", renderFleetExplain(&fleet.ExplainResult{
			Kind:    "service",
			Subject: hostile,
			Status:  hostile,
			Reasons: []fleet.Reason{{Code: hostile, Message: hostile}},
		})},
		{"lock path", renderLock(&app.LockResult{Path: hostile, Written: true})},
		{"impact service, consumers and owners", renderImpact(&impact.Result{
			Service:        hostile,
			OldVersion:     hostile,
			NewVersion:     hostile,
			Classification: hostile,
			Consumers: []impact.AffectedConsumer{
				{Service: hostile, Confidence: impact.Confidence(hostile)},
			},
			Owners: []string{hostile},
		})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Assert the payload reached the pane, so a renderer that dropped the
			// field entirely cannot pass this for the wrong reason.
			if !strings.Contains(tt.got, escaped) {
				t.Fatalf("escaped payload missing from the output, got:\n%q", tt.got)
			}
			// The pane legitimately carries this package's own lipgloss escapes, so
			// look for the payload's controls specifically rather than for any ESC.
			if strings.Contains(tt.got, hostile) || strings.Contains(tt.got, "\x1b[2K") ||
				strings.ContainsRune(tt.got, '\r') {
				t.Fatalf("raw control characters survived into the output, got:\n%q", tt.got)
			}
		})
	}
}

func TestRenderImpactWithBreakingChanges(t *testing.T) {
	r := &impact.Result{
		Service:        "test-svc",
		OldVersion:     "1.0.0",
		NewVersion:     "2.0.0",
		Classification: "BREAKING",
		BreakingChanges: []diff.Change{
			{Path: "interfaces", Type: diff.Removed, Reason: "removed", Classification: diff.Breaking},
		},
		PotentiallyBreakingChanges: []diff.Change{
			{Path: "state.scope", Type: diff.Modified, Reason: "changed", Classification: diff.PotentialBreaking},
		},
		Consumers: []impact.AffectedConsumer{
			{
				Service:              "consumer-svc",
				Direct:               true,
				Depth:                1,
				CompatibilityVerdict: impact.CompatibilityIncompatible,
				Confidence:           impact.ConfidenceContractual,
				Targets:              []string{"prod/Deployment/consumer"},
			},
		},
	}
	got := renderImpact(r)
	if !strings.Contains(got, "Breaking changes") {
		t.Fatal("render should include breaking changes section")
	}
	if !strings.Contains(got, "Potentially breaking changes") {
		t.Fatal("render should include potentially breaking changes section")
	}
	if !strings.Contains(got, "[direct]") {
		t.Fatal("render should mark direct consumers")
	}
}
