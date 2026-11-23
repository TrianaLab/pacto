package delivery

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var managedPayments = ServiceRef{Domain: "ghcr.io/acme", Name: "payments"}

func TestManagedStageNamesAndLabels(t *testing.T) {
	labels := ManagedLabels(managedPayments, "staging")
	want := map[string]string{ManagedLabel: "pacto", "delivery.pacto.trianalab.io/service": "payments", "delivery.pacto.trianalab.io/environment": "staging"}
	if !reflect.DeepEqual(labels, want) || ManagedLabel != "delivery.pacto.trianalab.io/managed-by" {
		t.Fatalf("labels %v", labels)
	}
	p := Placement{Namespace: "payments-staging", Provenance: PlacementObserved, Source: PlacementFromPacto, Evidence: "a Pacto runs there"}
	ann := PlacementAnnotations(DesiredFromLadder, p)
	for k, v := range map[string]string{"placement-provenance": "observed", "placement-source": "pacto",
		"placement-evidence": "a Pacto runs there", "environment-source": "ladder"} {
		if ann["delivery.pacto.trianalab.io/"+k] != v {
			t.Errorf("%s = %q", k, ann["delivery.pacto.trianalab.io/"+k])
		}
	}
}

func TestFirstRungTakesRegistry(t *testing.T) {
	s := ManagedStageSpec(managedPayments, "staging", nil, ApplyRef{Kind: ApplierArgoCD, Name: "payments"}, nil, false)
	if s.Candidates.Registry == nil || *s.Candidates.Registry != (RegistryCandidates{}) || s.Candidates.UpstreamStage != nil ||
		s.Service != managedPayments || s.Environment != "staging" || s.ApplyRef.Name != "payments" || s.Workload != nil {
		t.Fatalf("spec %+v", s)
	}
}

func TestLaterRungTakesUpstreamStage(t *testing.T) {
	up := &StageRef{Name: "payments-staging", Namespace: "payments-staging"}
	w := &WorkloadRef{Kind: "StatefulSet", Name: "api"}
	s := ManagedStageSpec(managedPayments, "production", up, ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}, w, false)
	if s.Candidates.Registry != nil || s.Candidates.UpstreamStage != up || s.Workload != w {
		t.Fatalf("spec %+v", s)
	}
}

func TestAdoptedUnguardedStartsObserve(t *testing.T) {
	s := ManagedStageSpec(managedPayments, "staging", nil, ApplyRef{Name: "payments"}, nil, false)
	if s.StartLevel != LevelObserve || s.Approval.Always {
		t.Fatalf("spec %+v", s)
	}
}

func TestAdoptedGuardedStartsApprovalGated(t *testing.T) {
	s := ManagedStageSpec(managedPayments, "staging", nil, ApplyRef{Name: "payments"}, nil, true)
	if s.StartLevel != LevelAutonomous || !s.Approval.Always {
		t.Fatalf("spec %+v", s)
	}
}

func TestPromotionOrderFromManagedLinks(t *testing.T) {
	stage := func(env, ns string, up *StageRef) StageObject {
		return StageObject{Metadata: ObjectMeta{Namespace: ns, Name: "payments-" + env},
			Spec: ManagedStageSpec(managedPayments, env, up, ApplyRef{Name: "payments"}, nil, false)}
	}
	stages := []StageObject{
		stage("production", "production", &StageRef{Name: "payments-staging", Namespace: "payments-staging"}),
		stage("staging", "payments-staging", nil),
	}
	if order, _, cycle, unordered := PromotionOrder(stages); !slices.Equal(order, []string{"staging", "production"}) || len(cycle) > 0 || len(unordered) > 0 {
		t.Fatalf("order %v", order)
	}
}

// TestNoGateReadsLadderOrPlacement keeps the installation ladder, placement
// and the managed label out of every gate: only the three files that build
// them may name them, so nothing inferred reaches a promote decision.
func TestNoGateReadsLadderOrPlacement(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	allowed := []string{"environments.go", "placement.go", "ladderrecord.go"}
	words := []string{"Placement", "LadderRung", "ManagedLabel", "delivery.pacto.trianalab.io/managed-by"}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || slices.Contains(allowed, f) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, w := range words {
			if strings.Contains(string(b), w) {
				t.Errorf("%s names %s", f, w)
			}
		}
	}
	if !slices.Contains(files, "plan.go") || scanned < 10 {
		t.Fatalf("scanned %d files of %v", scanned, files)
	}
}

func TestRetireAfterGrace(t *testing.T) {
	// D5: a temporary environment expiring 2026-11-20 is retired seven days later.
	expires := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if got := RetireAt(expires, 168*time.Hour); !got.Equal(time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("retired at %v", got)
	}
}

func TestStagePlacementReadsWhatPactoWrote(t *testing.T) {
	p := Placement{Namespace: "payments-staging", Provenance: PlacementInferred, Source: PlacementFromGitOps, Evidence: "Argo CD source github.com/acme/payments"}
	ann := PlacementAnnotations(DesiredFromLadder, p)
	ann[PlacementConfirmedByAnnotation] = "bob@acme.com"
	meta := ObjectMeta{Namespace: "payments-staging", Labels: ManagedLabels(ServiceRef{Name: "payments"}, "staging"), Annotations: ann}
	source, got, by, managed := StagePlacement(meta)
	if source != DesiredFromLadder || got != p || by != "bob@acme.com" || !managed {
		t.Fatalf("got %q %+v %q %v", source, got, by, managed)
	}
	source, got, by, managed = StagePlacement(ObjectMeta{Namespace: "shop", Annotations: ann})
	if source != PlacementFromStage || got != (Placement{Namespace: "shop", Provenance: PlacementDeclared, Source: PlacementFromStage}) || by != "" || managed {
		t.Fatalf("hand-written: %q %+v %q %v", source, got, by, managed)
	}
}
