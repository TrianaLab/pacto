package scenario

import (
	"slices"
	"strings"
	"testing"
)

// Parity between the two surfaces.
//
// One scenario is projected onto Kubernetes and onto Compose, and the demo is
// only worth distributing if it is the SAME demo: the same observation sources
// under the same identities, the same signer, the same published bundles all
// reachable, and the same facts owed — minus exactly what the platform cannot do.
//
// The tests below are deliberately written as extractions from the RENDERED
// projections rather than as comparisons of the fields they came from. Comparing
// the fields would be tautological; comparing the outputs catches a projection
// that stops carrying something it used to carry, which is the way these two
// would actually drift apart.
//
// Only ONE divergence is permitted, and it is not permitted implicitly: it is a
// Capability the Compose surface declares it does not provide, subtracted from
// the fact count and named in the gate's output.

// helmObservationSources is every observation source the chart is configured
// with, read back out of the `--set` arguments the harness passes.
func helmObservationSources(t *testing.T, s Scenario) []string {
	t.Helper()
	var out []string
	for _, v := range helmValues(t, s) {
		key, value, _ := strings.Cut(v, "=")
		if strings.HasPrefix(key, "dashboard.observation.sources[") && strings.HasSuffix(key, "].name") {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

// composeObservationSources is every observation source the dashboard container
// is configured with, read back out of its argument list.
func composeObservationSources(t *testing.T, s Scenario) []string {
	t.Helper()
	args := listOf(t, composeSvc(t, composeFileOf(t, s), "dashboard"), "command")
	var out []string
	for i, a := range args {
		if a != "--trace-source" || i+1 >= len(args) {
			continue
		}
		name, _, ok := strings.Cut(args[i+1], "=")
		if !ok {
			t.Errorf("--trace-source %q names no source", args[i+1])
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func TestParity_BothSurfacesObserveTheSameSourcesUnderTheSameIdentities(t *testing.T) {
	k8s, compose := helmObservationSources(t, OperationalGraph), composeObservationSources(t, OperationalGraph)
	if len(k8s) == 0 {
		t.Fatal("neither surface observes anything, so this proves nothing")
	}
	if !slices.Equal(k8s, compose) {
		t.Errorf("the surfaces observe different sources:\n  kubernetes %v\n  compose    %v", k8s, compose)
	}
	// A source identity is what the Product publishes it as, so a surface that
	// renamed one would show a different Data Source for the same export.
	s := mutate(func(s *Scenario) {
		old := s.Sources[2].ID
		s.Sources[2].ID = "renamed-traces"
		// Rename it everywhere, because that is what renaming a Data Source is: the
		// Compose projection now carries the export itself, and a relationship left
		// pointing at the old id would fail to project rather than project a rename.
		for i, rel := range s.Relationships {
			if rel.ObservedBy == old {
				s.Relationships[i].ObservedBy = "renamed-traces"
			}
		}
	})
	if got := composeObservationSources(t, s); slices.Equal(got, compose) {
		t.Errorf("renaming a source did not reach the Compose projection: still %v", got)
	}
	if got := helmObservationSources(t, s); slices.Equal(got, k8s) {
		t.Errorf("renaming a source did not reach the Helm projection: still %v", got)
	}
}

// WHO signs is one identity on both surfaces. Kubernetes learns it from the
// plan's signer record, which the harness runs keygen with; Compose bakes it into
// the container that mints the key. Signing as anyone else is rejected at
// ingestion, so a surface that drifted here would fail late and obscurely.
func TestParity_BothSurfacesSignAsTheSameProducer(t *testing.T) {
	plan, err := OperationalGraph.Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var producer, keyID string
	for line := range strings.SplitSeq(strings.TrimSpace(string(plan)), "\n") {
		if f := strings.Split(line, "\t"); f[0] == RecordSigner {
			if len(f) != 3 {
				t.Fatalf("signer record has %d fields: %q", len(f), line)
			}
			producer, keyID = f[1], f[2]
		}
	}
	if producer == "" {
		t.Fatal("the plan names no signer, so this proves nothing")
	}
	script := argsOf(t, composeSvc(t, composeFileOf(t, OperationalGraph), "evidence"), "command")
	if !strings.Contains(script, "--key-id "+keyID+" --producer "+producer) {
		t.Errorf("Compose mints a different identity from the one the plan signs with (%s/%s):\n%s", producer, keyID, script)
	}
	if !strings.Contains(script, "--producer "+producer+"\n") && !strings.HasSuffix(strings.TrimSpace(script), "--producer "+producer) {
		t.Errorf("the Evidence Server does not serve as %s:\n%s", producer, script)
	}
}

// Everything the fixture publishes is reachable on Compose, by one route or the
// other: the dashboard is pointed at it, or the Evidence Server produces a target
// for it. A bundle that is pushed and then reachable by neither is a published
// revision the Product could never show — which on Kubernetes would fail the gate
// and here would just be missing.
func TestParity_EveryPublishedBundleIsReachableOnCompose(t *testing.T) {
	plan, err := OperationalGraph.Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, OperationalGraph), "dashboard"), "command")
	subjects := map[string]bool{}
	for _, ev := range OperationalGraph.Evidence {
		if svc, ok := OperationalGraph.Service(ev.Service); ok {
			subjects[svc.Repo] = true
		}
	}
	named := strings.Fields(cmd)
	pushed := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(plan)), "\n") {
		f := strings.Split(line, "\t")
		if f[0] != RecordPush {
			continue
		}
		if len(f) != 4 {
			t.Fatalf("push record has %d fields: %q", len(f), line)
		}
		pushed++
		repo, _, _ := strings.Cut(f[3], ":")
		// The pinned revision, not just the repository. A dashboard told only the
		// repository resolves the newest tag and never learns the older ones exist,
		// which is how the Product gate came to see a revision the disk cache knew
		// about and the registry source did not.
		switch {
		case slices.Contains(named, "oci://"+ComposeDomain+"/"+f[3]):
		case subjects[repo]:
		default:
			t.Errorf("the demo publishes %s and then reaches it by neither the dashboard nor the Evidence Server", f[3])
		}
	}
	if pushed == 0 {
		t.Fatal("the plan publishes nothing, so this proves nothing")
	}
}

// Both surfaces store evidence against the SAME contract revisions.
//
// The registry is the evidence store, so the subject set is where the two demos
// would silently diverge: a Kubernetes run publishing referrers of one revision
// and a Compose run publishing them under another would each look complete and
// neither would be reading the other's evidence. Compared as EXTRACTIONS from
// the two rendered projections — the compose container's argument list against
// the chart's --set arguments — so a surface that stopped carrying a subject is
// caught rather than a shared derivation that both sides trivially agree on.
func TestParity_BothSurfacesStoreEvidenceAgainstTheSameSubjects(t *testing.T) {
	files, err := OperationalGraph.MaterializeFiles(ComposeDomain)
	if err != nil {
		t.Fatalf("MaterializeFiles: %v", err)
	}
	digests, err := OperationalGraph.Digests(files)
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	set, err := OperationalGraph.HelmEvidenceValues(ComposeDomain, digests)
	if err != nil {
		t.Fatalf("HelmEvidenceValues: %v", err)
	}
	var k8s []string
	for _, v := range set {
		key, value, _ := strings.Cut(v, "=")
		if strings.HasPrefix(key, "evidence.registry.subjects[") {
			k8s = append(k8s, value)
		}
	}
	slices.Sort(k8s)
	compose := composeEvidenceSubjects(t, OperationalGraph)
	if len(k8s) == 0 {
		t.Fatal("neither surface stores against any subject, so this proves nothing")
	}
	if !slices.Equal(k8s, compose) {
		t.Errorf("the surfaces store evidence against different revisions:\n  kubernetes %v\n  compose    %v", k8s, compose)
	}
}

// The surfaces owe DIFFERENT numbers of facts, and the difference is accounted
// for to the fact: one operational target per running service, and nothing else.
// This is the assertion that stops a Compose leg from quietly becoming a weaker
// run — the gate would still print "N/N facts" while N had shrunk for a reason
// nobody declared.
func TestParity_TheOnlyFactsComposeDoesNotOweAreTheOnesItCannotProduce(t *testing.T) {
	running := 0
	for _, svc := range OperationalGraph.Services {
		if svc.Workload != nil {
			running++
		}
	}
	if running == 0 {
		t.Fatal("nothing runs in the fixture, so this proves nothing")
	}
	k8s := OperationalGraph.FactCount(SurfaceKubernetes)
	compose := OperationalGraph.FactCount(SurfaceCompose)
	if k8s-compose != running {
		t.Errorf("kubernetes owes %d facts and compose %d, a difference of %d; the only permitted difference is the %d operational targets Compose has no controller to reconcile",
			k8s, compose, k8s-compose, running)
	}
	// And the difference is DECLARED, not just arithmetic: the surface says which
	// capability it lacks, so the gate can name it instead of printing a smaller
	// denominator.
	if got := SurfaceCompose.Missing(); !slices.Equal(got, []Capability{CapabilityOperationalTarget}) {
		t.Errorf("Compose declares it is missing %v, which does not explain the %d-fact difference", got, k8s-compose)
	}
}
