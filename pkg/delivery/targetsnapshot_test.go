package delivery

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func targetSnapshot() TargetSnapshot {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name = "billing", "ledger"
	return TargetSnapshot{Cluster: "6f1e-uid", AsOf: "2026-11-17T10:00:00Z", Completeness: fleet.CompletenessComplete,
		RegistryMode: "referrers=native listing=tags", Publisher: "key:abc", Pactos: []PactoView{p},
		Workloads: []fleet.RawInferredService{{Namespace: "billing", Kind: "Deployment", Name: "cron"}}}
}

// TestNewTargetSnapshotStatement: the statement names the cluster's tag by the
// sha256 of the snapshot it carries.
func TestNewTargetSnapshotStatement(t *testing.T) {
	s := targetSnapshot()
	st := NewTargetSnapshotStatement(s)
	body, _ := json.Marshal(s)
	want := []Subject{{Name: "pacto-targets:cluster-6f1e-uid", Digest: map[string]string{"sha256": sha256Hex(body)}}}
	if st.Type != StatementType || st.PredicateType != PredicateTargetSnapshot || !reflect.DeepEqual(st.Subject, want) ||
		!reflect.DeepEqual(st.Predicate, s) {
		t.Fatalf("statement = %+v", st)
	}
}

// TestSignTargetSnapshotRoundTrip: the signed payload decodes back strictly to
// the same statement; a signer that fails is an error.
func TestSignTargetSnapshotRoundTrip(t *testing.T) {
	st := NewTargetSnapshotStatement(targetSnapshot())
	b, err := SignTargetSnapshot(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	var got TargetSnapshotStatement
	if _, err := openStatement(b, PredicateTargetSnapshot, &got); err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("opened %+v, %v", got, err)
	}
	if _, err := SignTargetSnapshot(t.Context(), failingSigner{}, st); err == nil {
		t.Fatal("a failing signer signed")
	}
}

func TestTargetSnapshotTag(t *testing.T) {
	if tag, err := TargetSnapshotTag("6f1e-uid"); err != nil || tag != "cluster-6f1e-uid" {
		t.Fatalf("tag = %q, %v", tag, err)
	}
	for _, bad := range []string{"", "a/b", strings.Repeat("a", 101), "a.b"} {
		if _, err := TargetSnapshotTag(bad); err == nil {
			t.Errorf("cluster %q accepted", bad)
		}
	}
}

func TestTargetSnapshotCheck(t *testing.T) {
	if err := targetSnapshot().Check(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*TargetSnapshot){
		"cluster":      func(s *TargetSnapshot) { s.Cluster = "a/b" },
		"asOf":         func(s *TargetSnapshot) { s.AsOf = "yesterday" },
		"completeness": func(s *TargetSnapshot) { s.Completeness = fleet.CompletenessEmpty },
		"publisher":    func(s *TargetSnapshot) { s.Publisher = "" },
	} {
		s := targetSnapshot()
		edit(&s)
		if err := s.Check(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
