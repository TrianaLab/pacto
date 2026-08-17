package scenario

import (
	"strings"
	"testing"
)

func helmValues(t *testing.T, s Scenario) []string {
	t.Helper()
	v, err := s.HelmValues()
	if err != nil {
		t.Fatalf("HelmValues: %v", err)
	}
	return v
}

func TestHelmValues_ConfigureTheDeclaredObservationSource(t *testing.T) {
	want := []string{
		"dashboard.observation.sources[0].name=orders-traces",
		"dashboard.observation.sources[0].file=traces.json",
		"dashboard.observation.sources[0].configMap=pacto-orders-traces",
	}
	got := helmValues(t, OperationalGraph)
	if len(got) != len(want) {
		t.Fatalf("HelmValues() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("HelmValues()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The values follow the DECLARATION. This is the same counterexample the plan
// has for the observation source's identity, one layer up: rename the source and
// the chart must be told the new name, not the old one.
func TestHelmValues_FollowTheObservationSourceIdentity(t *testing.T) {
	before := strings.Join(helmValues(t, OperationalGraph), "\n")
	after := strings.Join(helmValues(t, mutate(func(s *Scenario) {
		for i := range s.Sources {
			if s.Sources[i].Kind == SourceObservation {
				s.Sources[i].ID = "renamed-traces"
			}
		}
	})), "\n")
	moved(t, before, after, "orders-traces", "renamed-traces")
}

// The chart indexes its sources array from zero and contiguously, which is NOT
// the position of the source in the scenario: registry and cache sources are
// declared alongside the observation ones and take no chart slot. Indexing by
// the outer position would leave a hole, and helm --set fills a hole in an array
// with a null the chart then renders as an empty source.
func TestHelmValues_IndexTheChartArrayNotTheScenario(t *testing.T) {
	s := mutate(func(s *Scenario) {
		s.Sources = append(s.Sources, Source{ID: "returns-traces", Kind: SourceObservation})
	})
	got := helmValues(t, s)
	want := []string{
		"dashboard.observation.sources[0].name=orders-traces",
		"dashboard.observation.sources[0].file=traces.json",
		"dashboard.observation.sources[0].configMap=pacto-orders-traces",
		"dashboard.observation.sources[1].name=returns-traces",
		"dashboard.observation.sources[1].file=traces.json",
		"dashboard.observation.sources[1].configMap=pacto-returns-traces",
	}
	if len(got) != len(want) {
		t.Fatalf("HelmValues() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("HelmValues()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The ConfigMap the harness CREATES and the ConfigMap the chart is TOLD ABOUT
// come from two projections of the same scenario. If they ever disagree the run
// still passes every unit test and then mounts nothing: the operator waits on a
// ConfigMap no one made while the one that exists is read by nobody.
func TestHelmValues_NameTheConfigMapsThePlanCreates(t *testing.T) {
	plan, err := OperationalGraph.Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	values := strings.Join(helmValues(t, OperationalGraph), "\n")
	n := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(plan)), "\n") {
		f := strings.Split(line, "\t")
		if f[0] != RecordObservation {
			continue
		}
		n++
		for key, want := range map[string]string{"name": f[1], "configMap": f[2], "file": f[3]} {
			if !strings.Contains(values, "]."+key+"="+want+"\n") && !strings.HasSuffix(values, "]."+key+"="+want) {
				t.Errorf("the plan creates %s=%q, but no chart value carries it:\n%s", key, want, values)
			}
		}
	}
	if n == 0 {
		t.Fatal("the plan has no observation record, so this proves nothing")
	}
	if got := len(helmValues(t, OperationalGraph)); got != n*3 {
		t.Errorf("the plan has %d observation records and the chart is told %d values, want %d", n, got, n*3)
	}
}

// A scenario the chart cannot be configured for must SAY so. The Kind harness
// turns these into --set arguments; an empty list would install a dashboard with
// no observation source at all and then wait out its timeout on a fact nothing
// was ever configured to produce.
func TestHelmValues_RefuseAScenarioWithNothingToObserve(t *testing.T) {
	_, err := mutate(func(s *Scenario) {
		var kept []Source
		for _, src := range s.Sources {
			if src.Kind != SourceObservation {
				kept = append(kept, src)
			}
		}
		s.Sources = kept
	}).HelmValues()
	if err == nil {
		t.Fatal("a scenario with no observation source produced chart values anyway")
	}
	if !strings.Contains(err.Error(), OperationalGraph.Name) {
		t.Errorf("the refusal %q does not name the scenario", err)
	}
}

// helm --set has a grammar over the VALUE too: a comma ends the assignment, a
// backslash escapes, brackets and = address into the structure. A source id
// carrying one would configure something other than what it names — the same
// forgery the plan's tab check refuses, in the other consumer.
func TestHelmValues_RefuseAValueThatCouldForgeAnAssignment(t *testing.T) {
	for _, bad := range []string{"a,b", "a=b", "a[0]", `a\b`, "a\tb", "a\nb", ""} {
		_, err := mutate(func(s *Scenario) {
			for i := range s.Sources {
				if s.Sources[i].Kind == SourceObservation {
					s.Sources[i].ID = bad
				}
			}
		}).HelmValues()
		if err == nil {
			t.Errorf("the source id %q was accepted into a --set argument", bad)
		}
	}
}
