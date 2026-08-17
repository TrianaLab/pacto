package scenario

import (
	"strings"
	"testing"
)

// materialized renders the fixture into a temporary directory and returns it.
func materialized(t *testing.T, s Scenario) string {
	t.Helper()
	dir := t.TempDir()
	if err := s.Materialize(dir, ComposeDomain); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return dir
}

// The digests are keyed the way the projections LOOK THEM UP. A map that is right
// about every value and wrong about one key is a projection that refuses to
// render, and this is the test that says so rather than the harness discovering
// it at run time.
func TestDigests_AreWhatTheProjectionsAskFor(t *testing.T) {
	dir := materialized(t, OperationalGraph)
	digests, err := OperationalGraph.Digests(dir)
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	if _, err := OperationalGraph.EvidencePayloads(ComposeArtifactMount, ComposeDomain, digests); err != nil {
		t.Errorf("the evidence payloads could not be pinned to the computed digests: %v", err)
	}
	if _, err := OperationalGraph.PactoCRs("demo", ComposeDomain, digests); err != nil {
		t.Errorf("the Pacto CRs could not be pinned to the computed digests: %v", err)
	}
	for key, d := range digests {
		if !strings.HasPrefix(d, "sha256:") || len(d) != len("sha256:")+64 {
			t.Errorf("%s got %q, which is not an OCI digest", key, d)
		}
	}
	if len(digests) == 0 {
		t.Fatal("the fixture publishes nothing, so this proves nothing")
	}
}

// Deterministic, and determined by the CONTENT. The artifact bakes these in
// before any registry exists, so a digest that moved between two runs of the same
// bytes — or stayed put across different bytes — would put the demo's evidence on
// content that is not there.
func TestDigests_FollowTheBytesAndNothingElse(t *testing.T) {
	dir := materialized(t, OperationalGraph)
	first, err := OperationalGraph.Digests(dir)
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	second, err := OperationalGraph.Digests(materialized(t, OperationalGraph))
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	for key, d := range first {
		if second[key] != d {
			t.Errorf("%s digests to %s and then to %s from the same bytes", key, d, second[key])
		}
	}
	changed := mutate(func(s *Scenario) {
		s.Services[1].Revisions[0].Files["pacto.yaml"] += "\n# one more comment\n"
	})
	third, err := changed.Digests(materialized(t, changed))
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	same := 0
	for key, d := range first {
		if third[key] == d {
			same++
		}
	}
	if same == len(first) {
		t.Error("changing a bundle's content changed no digest")
	}
	if same == 0 {
		t.Error("changing one bundle changed every digest; they are not content-addressed")
	}
}

// A revision the fixture declares but nothing materialized has no digest, and
// saying so beats returning a map the caller then fails to find a key in.
func TestDigests_RefuseToInventOneForAMissingBundle(t *testing.T) {
	if _, err := OperationalGraph.Digests(t.TempDir()); err == nil {
		t.Error("digests were computed for bundles that were never written")
	}
	if _, err := OperationalGraph.Digests(""); err == nil {
		t.Error("digests were computed from no directory at all")
	}
}
