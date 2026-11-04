package fleet

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func envTargetFleet(t *testing.T) *Query {
	t.Helper()
	fsys := fstest.MapFS{
		"app.json":             {Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}},"required":["replicas"]}`)},
		"envs/staging.yaml":    {Data: []byte("app:\n  replicas: 2\n")},
		"envs/production.yaml": {Data: []byte("app:\n  replicas: 3\n")},
	}
	env := func(v string) map[string]string { return map[string]string{TargetEnvironmentLabel: v} }
	tgt := func(name, digest string, labels map[string]string) RawTarget {
		return RawTarget{Scope: "ns", Kind: "k8s", Name: name, Service: "env-svc", Digest: digest, Compliance: StatusCompliant, Labels: labels}
	}
	a := NewMemorySource("a", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML), RawYAML: []byte(envLeafYAML), FS: fsys}, Digest: "sha256:env"}},
		Targets: []RawTarget{
			tgt("unlabelled", "sha256:env", nil),
			tgt("staging", "sha256:env", env("staging")),
			tgt("prod", "sha256:env", env("prod")),
			tgt("unlinked", "sha256:nope", env("staging")),
			tgt("disputed", "sha256:env", env("staging")),
			tgt("late", "sha256:env", map[string]string{"tier": "gold"}),
		},
	})
	b := NewMemorySource("b", "local", &Collection{Targets: []RawTarget{tgt("disputed", "sha256:env", env("prod")), tgt("late", "sha256:env", env("staging"))}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestTargetEnvironment_EveryCase(t *testing.T) {
	q := envTargetFleet(t)
	cases := []struct {
		name, env, declared, reason string
		check                       bool
	}{
		{"unlabelled", "", "unknown", "No source reports this target's environment. Pacto reads it from the delivery.pacto.trianalab.io/environment label.", false},
		{"disputed", "", "unknown", "Sources disagree on this target's environment label.", false},
		{"unlinked", "staging", "unknown", "Pacto cannot tell which revision runs here, so it cannot check the environment against a contract.", false},
		{"staging", "staging", "declared", "", true},
		{"late", "staging", "declared", "", true},
		{"prod", "prod", "not-declared", "Revision env-svc 1.0.0 does not declare environment prod.", false},
	}
	for _, c := range cases {
		key := string(NewTargetKey("ns", "k8s", c.name))
		d, err := q.EntityDetail(KindTarget, key)
		if err != nil {
			t.Fatal(err)
		}
		got := d.Target.Environment
		if got.Name != c.env || got.Declared != c.declared || got.Reason != c.reason || (got.Check != nil) != c.check {
			t.Errorf("%s: environment = %+v", c.name, got)
		}
		if c.check && (got.Check.Name != "staging" || got.Check.Status != EnvironmentPass) {
			t.Errorf("%s: check = %+v", c.name, got.Check)
		}
		if d.Entity.Environment != c.env {
			t.Errorf("%s: ref environment = %q, want %q", c.name, d.Entity.Environment, c.env)
		}
	}
}

func TestEntities_EnvironmentFilter(t *testing.T) {
	q := envTargetFleet(t)
	got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindTarget}, Environment: "staging", Sort: SortName})
	if len(got) != 3 || got[0] != "ns/k8s/late" || got[2] != "ns/k8s/unlinked" {
		t.Errorf("staging targets = %v", got)
	}
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Environment: "staging"}); !errors.As(err, &iq) {
		t.Errorf("an environment filter on services = %v, want InvalidQueryError", err)
	}
}

// A disputed environment label is dropped, so a label search cannot match the
// value Pacto refuses to name, and a later source cannot bring it back.
func TestTargetEnvironment_ConflictDropsLabel(t *testing.T) {
	tgt := func(v string) *Collection {
		return &Collection{Targets: []RawTarget{{Scope: "ns", Kind: "k8s", Name: "x", Service: "x",
			Labels: map[string]string{TargetEnvironmentLabel: v, "tier": "gold"}}}}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("a", "local", tgt("staging")), NewMemorySource("b", "local", tgt("prod")), NewMemorySource("c", "local", tgt("staging")))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	rec := snap.Targets[NewTargetKey("ns", "k8s", "x")]
	if _, ok := rec.Labels[TargetEnvironmentLabel]; ok || rec.Environment != "" || rec.Labels["tier"] != "gold" {
		t.Errorf("target = env %q, labels %v", rec.Environment, rec.Labels)
	}
	res, err := q.Search(SearchFilter{Labels: map[string]string{TargetEnvironmentLabel: "staging"}})
	if err != nil || len(res.Services) != 0 {
		t.Errorf("label search on the disputed value = %+v, %v", res, err)
	}
	d, err := q.EntityDetail(KindTarget, string(rec.Key))
	if err != nil {
		t.Fatal(err)
	}
	if d.Target.Environment.Declared != "unknown" || d.Target.Environment.Reason != "Sources disagree on this target's environment label." {
		t.Errorf("environment = %+v", d.Target.Environment)
	}
}
