package delivery

import (
	"encoding/json"
	"reflect"
	"slices"
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

// TestTargetSnapshotCheckSources: every section has a known kind, once, an
// RFC 3339 asOf and a fleet completeness; the evidence section is bounded.
func TestTargetSnapshotCheckSources(t *testing.T) {
	ok := targetSnapshot()
	ok.Sources = []SnapshotSource{{Kind: fleet.SourceKindKubernetes, AsOf: ok.AsOf, Completeness: fleet.CompletenessComplete},
		{Kind: fleet.SourceKindEvidenceHTTP, AsOf: ok.AsOf, Completeness: fleet.CompletenessPartial}}
	ok.Evidence = json.RawMessage(`{}`)
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*TargetSnapshot){
		"kind":         func(s *TargetSnapshot) { s.Sources[1].Kind = "mira" },
		"kind \"kub":   func(s *TargetSnapshot) { s.Sources[1].Kind = fleet.SourceKindKubernetes },
		"asOf":         func(s *TargetSnapshot) { s.Sources[0].AsOf = "now" },
		"completeness": func(s *TargetSnapshot) { s.Sources[1].Completeness = "" },
		"evidence":     func(s *TargetSnapshot) { s.Evidence = make(json.RawMessage, 4<<20+1) },
	} {
		s := ok
		s.Sources = slices.Clone(ok.Sources)
		edit(&s)
		if err := s.Check(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestTargetSnapshotOwnerPolicyRoundTrip: the owner policy travels in the
// signed snapshot and decodes back strictly.
func TestTargetSnapshotOwnerPolicyRoundTrip(t *testing.T) {
	s := targetSnapshot()
	p := ownerPolicy()
	s.OwnerPolicy = &p
	st := NewTargetSnapshotStatement(s)
	b, err := SignTargetSnapshot(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := OpenTargetSnapshot(b, nil, nil)
	if err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("opened %+v, %v", got, err)
	}
}

// TestTargetSnapshotRefusesOpenOwnerRule: a snapshot whose owner policy holds
// a rule that matches anyone is refused, by Check and when opened.
func TestTargetSnapshotRefusesOpenOwnerRule(t *testing.T) {
	s := targetSnapshot()
	p := ownerPolicy()
	p.DefaultApprovers[0].Subject = ".*"
	s.OwnerPolicy = &p
	if err := s.Check(); err == nil || !strings.Contains(err.Error(), "owner policy") {
		t.Fatalf("err = %v", err)
	}
	b, err := SignTargetSnapshot(t.Context(), plainSigner{}, NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenTargetSnapshot(b, nil, nil); err == nil {
		t.Fatal("an open owner rule opened")
	}
}

// TestTargetSnapshotWithoutOwnerPolicyUnchanged: a snapshot with no owner
// policy encodes with no ownerPolicy key, and decodes and re-encodes byte
// identical.
func TestTargetSnapshotWithoutOwnerPolicyUnchanged(t *testing.T) {
	body, err := json.Marshal(targetSnapshot())
	if err != nil || strings.Contains(string(body), "ownerPolicy") {
		t.Fatalf("body %s, %v", body, err)
	}
	var back TargetSnapshot
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(back)
	if string(again) != string(body) {
		t.Fatalf("re-encoded %s, want %s", again, body)
	}
}
