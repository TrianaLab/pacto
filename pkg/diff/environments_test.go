package diff

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestDiffEnvironments(t *testing.T) {
	old := []contract.Environment{{Name: "staging", Values: "envs/staging.yaml"}, {Name: "qa", Values: "envs/qa.yaml"}}
	new := []contract.Environment{{Name: "staging", Values: "envs/staging.yaml"}, {Name: "production", Values: "envs/production.yaml"}}
	oldFS := fstest.MapFS{"envs/staging.yaml": {Data: []byte("app: {LOG_LEVEL: info}\nchart: {replicaCount: 2}\n")}}
	newFS := fstest.MapFS{"envs/staging.yaml": {Data: []byte("app: {LOG_LEVEL: warn}\nchart: {replicaCount: 2, ingress: {host: s.example.com}}\n")}}

	var got []string
	for _, c := range diffEnvironments(old, new, oldFS, newFS) {
		got = append(got, c.Path+" "+c.Type.String()+" "+c.Classification.String())
	}
	want := "environments[staging].values.app.LOG_LEVEL modified NON_BREAKING\n" +
		"environments[staging].values.chart.ingress added NON_BREAKING\n" +
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
	old := &contract.Contract{Environments: []contract.Environment{{Name: "staging", Values: "s.yaml"}}}
	r := Compare(t.Context(), old, &contract.Contract{}, nil, nil)
	if r.Classification != PotentialBreaking || len(r.Changes) != 1 || r.Changes[0].Path != "environments" {
		t.Fatalf("got %+v", r)
	}
}
