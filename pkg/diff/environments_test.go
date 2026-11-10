package diff

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestDiffEnvironments(t *testing.T) {
	old := []contract.Environment{{Name: "staging", File: "envs/staging.yaml"}, {Name: "qa", File: "envs/qa.yaml"}}
	new := []contract.Environment{{Name: "staging", File: "envs/staging.yaml"}, {Name: "production", File: "envs/production.yaml"}}
	oldFS := fstest.MapFS{"envs/staging.yaml": {Data: []byte("app: {LOG_LEVEL: info}\nchart: {replicaCount: 2}\n")}}
	newFS := fstest.MapFS{"envs/staging.yaml": {Data: []byte("app: {LOG_LEVEL: warn}\nchart: {replicaCount: 2, ingress: {host: s.example.com}}\n")}}

	var got []string
	for _, c := range diffEnvironments(old, new, oldFS, newFS) {
		got = append(got, c.Path+" "+c.Type.String()+" "+c.Classification.String())
	}
	want := "environments[staging].app.LOG_LEVEL modified NON_BREAKING\n" +
		"environments[staging].chart.ingress added NON_BREAKING\n" +
		"environments removed POTENTIAL_BREAKING\n" +
		"environments added NON_BREAKING"
	if strings.Join(got, "\n") != want {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}

	// A values file missing on one side compares as empty, and without a
	// bundle nothing is read.
	if c := diffEnvironments(old[:1], new[:1], fstest.MapFS{}, newFS); len(c) != 2 {
		t.Fatalf("missing old file: %+v", c)
	}
	if c := diffEnvironments(old[:1], new[:1], nil, nil); len(c) != 0 {
		t.Fatalf("no bundle: %+v", c)
	}
}

func TestCompare_Environments(t *testing.T) {
	old := &contract.Contract{Environments: []contract.Environment{{Name: "staging", File: "s.yaml"}}}
	r := Compare(t.Context(), old, &contract.Contract{}, nil, nil)
	if r.Classification != PotentialBreaking || len(r.Changes) != 1 || r.Changes[0].Path != "environments" {
		t.Fatalf("got %+v", r)
	}
}

func TestDiffEnvironmentFields(t *testing.T) {
	old := []contract.Environment{
		{Name: "a", File: "envs/a.yaml"},
		{Name: "b", File: "envs/b.yaml", Lifetime: "temporary", Expires: "2026-11-20T00:00:00Z", Owner: &contract.Owner{Team: "perf"}},
		{Name: "c", File: "envs/c.yaml", Lifetime: "temporary", Owner: &contract.Owner{Team: "x"}},
		{Name: "d", File: "envs/d.yaml", Lifetime: "persistent", Owner: &contract.Owner{Team: "same"}},
	}
	new := []contract.Environment{
		{Name: "a", File: "envs/a2.yaml", Lifetime: "temporary", Owner: &contract.Owner{Team: "perf"}},
		{Name: "b", File: "envs/b.yaml", Lifetime: "persistent", Expires: "2026-12-01T00:00:00Z", Owner: &contract.Owner{Team: "core"}},
		{Name: "c", File: "envs/c.yaml"},
		{Name: "d", File: "envs/d.yaml", Lifetime: "persistent", Owner: &contract.Owner{Team: "same"}},
	}
	var got []string
	for _, c := range diffEnvironments(old, new, nil, nil) {
		got = append(got, c.Path+" "+c.Type.String()+" "+c.Classification.String())
	}
	want := "environments[a].file modified NON_BREAKING\n" +
		"environments[a].lifetime added NON_BREAKING\n" +
		"environments[a].owner added NON_BREAKING\n" +
		"environments[b].lifetime modified NON_BREAKING\n" +
		"environments[b].expires modified NON_BREAKING\n" +
		"environments[b].owner modified NON_BREAKING\n" +
		"environments[c].lifetime removed NON_BREAKING\n" +
		"environments[c].owner removed NON_BREAKING"
	if strings.Join(got, "\n") != want {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
	// Owner values are the display strings.
	if c := diffEnvironments(old[1:2], new[1:2], nil, nil)[2]; c.OldValue != "perf" || c.NewValue != "core" {
		t.Fatalf("owner values: %+v", c)
	}
	// Expires added and removed are NonBreaking too.
	e := []contract.Environment{{Name: "e", File: "f"}}
	ex := []contract.Environment{{Name: "e", File: "f", Expires: "2026-11-20T00:00:00Z"}}
	for _, c := range append(diffEnvironments(e, ex, nil, nil), diffEnvironments(ex, e, nil, nil)...) {
		if c.Classification != NonBreaking {
			t.Fatalf("expires: %+v", c)
		}
	}
}
