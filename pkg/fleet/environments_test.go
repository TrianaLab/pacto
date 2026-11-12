package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func envs(names ...string) []contract.Environment {
	out := make([]contract.Environment, len(names))
	for i, n := range names {
		out[i] = contract.Environment{Name: n, File: "envs/" + n + ".yaml"}
	}
	return out
}

func TestEnvironmentResultsNotRun(t *testing.T) {
	if EnvironmentResults(nil, nil) != nil {
		t.Error("no environments, no results")
	}
	r := EnvironmentResults(envs("staging"), nil)
	if r[0].Status != EnvironmentSkip || r[0].Reason != notReadReason || r[0].File != "envs/staging.yaml" {
		t.Errorf("unread document = %+v", r[0])
	}
	stages := append([]validation.Stage{{Name: "policy", Status: validation.StageFail}}, validation.SkippedStages("policy failed", "environments")...)
	if r := EnvironmentResults(envs("staging"), stages); r[0].Status != EnvironmentSkip || r[0].Reason != "policy failed" {
		t.Errorf("skipped stage = %+v", r[0])
	}
}

func TestEnvironmentResultsPerEnvironment(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	stages := []validation.Stage{
		// A cross-field error about environment 0, from an earlier stage.
		{Name: "cross-field", Status: validation.StagePass, Warnings: []contract.ValidationWarning{{Path: "environmentsX", Code: "OTHER"}}},
		{Name: "environments", Status: validation.StageFail,
			Errors: []contract.ValidationError{
				{Path: "environments[0].file", Code: "ENVIRONMENT_VALUES_INVALID", Message: "bad", File: "envs/a.yaml", Line: 3, Column: 5},
				{Path: "environments[10].file", Code: "ENVIRONMENT_UNKNOWN_CONFIGURATION", Message: "unknown"},
			},
			Warnings: []contract.ValidationWarning{
				{Path: "environments[1].file", Code: "ENVIRONMENT_VALUES_UNVERIFIED", Message: "cannot check"},
				{Path: "environments[2]", Code: "SOMETHING_ELSE", Message: "note"},
			}},
	}
	r := EnvironmentResults(envs(names...), stages)
	want := map[int]string{0: EnvironmentFail, 1: EnvironmentUnverified, 2: EnvironmentPass, 3: EnvironmentPass, 10: EnvironmentFail}
	for i, w := range want {
		if r[i].Status != w {
			t.Errorf("environment %d status %q, want %q", i, r[i].Status, w)
		}
	}
	f := r[0].Findings.Items[0]
	if r[0].Findings.Total != 1 || f.File != "envs/a.yaml" || f.Line != 3 || f.Column != 5 || f.Severity != "error" {
		t.Errorf("located finding = %+v", f)
	}
	if r[1].Findings.Items[0].Severity != "warning" || r[2].Findings.Count != 1 || r[3].Findings.Count != 0 {
		t.Errorf("findings: b %+v c %+v d %+v", r[1].Findings, r[2].Findings, r[3].Findings)
	}
}

func TestEnvironmentResultsBounded(t *testing.T) {
	st := validation.Stage{Name: "environments", Status: validation.StageFail}
	for i := range MaxDetailPreview + 5 {
		st.Errors = append(st.Errors, contract.ValidationError{Path: "environments[0].file", Code: "ENVIRONMENT_VALUES_INVALID", Message: fmt.Sprint(i)})
	}
	f := EnvironmentResults(envs("a"), []validation.Stage{st})[0].Findings
	if f.Total != MaxDetailPreview+5 || f.Count != MaxDetailPreview || !f.Truncated {
		t.Errorf("findings preview = total %d count %d truncated %v", f.Total, f.Count, f.Truncated)
	}
	many := make([]EnvironmentResult, MaxDetailPreview+1)
	for i := range many {
		many[i].Status = EnvironmentPass
	}
	many[0].Findings.Items = []LocatedFinding{{Message: "x"}}
	// The counts cover every environment: the last one, past the preview, failed.
	many[MaxDetailPreview].Status, many[1].Status, many[2].Status = EnvironmentFail, EnvironmentUnverified, EnvironmentSkip
	p := environmentsPreview(many)
	if p.Total != MaxDetailPreview+1 || p.Count != MaxDetailPreview || !p.Truncated || p.Failed != 1 || p.Unverified != 1 || p.Skipped != 1 {
		t.Errorf("environments preview = total %d count %d failed %d unverified %d skipped %d", p.Total, p.Count, p.Failed, p.Unverified, p.Skipped)
	}
	p.Items[0].Findings.Items[0].Message = "changed"
	if many[0].Findings.Items[0].Message != "x" {
		t.Error("the preview aliases the revision's findings")
	}
}

const envLeafYAML = `pactoVersion: '2.3'
service:
  name: env-svc
  version: 1.0.0
  owner:
    team: platform
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
configurations:
- name: app
  schema: app.json
  required: true
environments:
- name: staging
  file: envs/staging.yaml
- name: production
  file: envs/production.yaml
`

func TestBuildEnvironments(t *testing.T) {
	fsys := fstest.MapFS{
		"app.json":             {Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}},"required":["replicas"]}`)},
		"envs/staging.yaml":    {Data: []byte("app:\n  replicas: 2\n")},
		"envs/production.yaml": {Data: []byte("app:\n  replicas: many\n")},
	}
	readable := RawRevision{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML), RawYAML: []byte(envLeafYAML), FS: fsys}, Digest: "sha256:env"}
	unread := RawRevision{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML)}, Digest: "sha256:env"}
	// The unreadable contribution arrives first; the readable one fills in its verdicts.
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("a", "local", &Collection{Revisions: []RawRevision{unread}}),
		NewMemorySource("b", "local", &Collection{Revisions: []RawRevision{readable}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	var key RevisionKey
	for k := range snap.Revisions {
		key = k
	}
	d, err := q.EntityDetail(KindRevision, string(key))
	if err != nil {
		t.Fatal(err)
	}
	rd := d.Revision
	if len(rd.Stages) != 5 || rd.Stages[4].Name != "environments" || rd.Stages[4].Status != validation.StageFail {
		t.Fatalf("stages = %+v", rd.Stages)
	}
	staging, prod := rd.Environments.Items[0], rd.Environments.Items[1]
	if staging.Status != EnvironmentPass || prod.Status != EnvironmentFail {
		t.Fatalf("staging %+v production %+v", staging, prod)
	}
	if f := prod.Findings.Items[0]; f.File != "envs/production.yaml" || f.Line != 2 || !strings.Contains(f.Message, "production") {
		t.Errorf("production finding = %+v", f)
	}

	// A revision whose document nobody could read says nothing was checked.
	snap, _ = Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("a", "local", &Collection{Revisions: []RawRevision{unread}}))
	for _, rev := range snap.Revisions {
		if len(rev.Stages) != 0 || rev.Environments[0].Status != EnvironmentSkip {
			t.Errorf("unread revision = stages %+v environments %+v", rev.Stages, rev.Environments)
		}
	}
}

// A stage that ran and could not verify everything is skipped like one that
// never ran, but says so: only it carries the warnings that name what it could
// not check.
func TestStageSummariesMarkUnverified(t *testing.T) {
	got := StageSummaries([]validation.Stage{
		{Name: "environments", Status: validation.StageSkip, Reason: "could not verify: prod",
			Warnings: []contract.ValidationWarning{{Code: "ENVIRONMENT_VALUES_UNVERIFIED"}}},
		{Name: "policy", Status: validation.StageSkip, Reason: "structural failed"},
		{Name: "structural", Status: validation.StagePass, Warnings: []contract.ValidationWarning{{Code: "W"}}},
	})
	if !got[0].Unverified || got[1].Unverified || got[2].Unverified {
		t.Fatalf("unverified marks: %+v", got)
	}
}

func TestEnvironmentResultsLifetimeNotDeclared(t *testing.T) {
	for _, r := range EnvironmentResults(envs("staging"), nil) {
		if r.Lifetime != LifetimeNotDeclared || r.Expires != "" || r.Owner != nil || r.Expired {
			t.Errorf("an environment that declares nothing: %+v", r)
		}
	}
}

func TestEnvironmentResultsDeclaredFields(t *testing.T) {
	owner := &contract.Owner{Team: "previews", Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}}}
	in := []contract.Environment{{Name: "pr-1", File: "pr-1.yaml", Lifetime: contract.EnvironmentTemporary, Expires: "2026-01-01T00:00:00Z", Owner: owner}}
	r := EnvironmentResults(in, nil)[0]
	if r.Lifetime != "temporary" || r.Expires != "2026-01-01T00:00:00Z" || r.Owner == nil || r.Owner.Team != "previews" {
		t.Fatalf("declared fields: %+v", r)
	}
	r.Owner.Contacts[0].Value = "x"
	if owner.Contacts[0].Value != "a@b.c" {
		t.Error("the result's owner aliases the contract's")
	}
}

func TestMarkExpired(t *testing.T) {
	asOf := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner := &contract.Owner{Team: "a", Contacts: []contract.OwnerContact{{Value: "v"}}}
	in := []EnvironmentResult{
		{Name: "past", Expires: "2026-07-01T00:00:00Z", Owner: owner},
		{Name: "equal", Expires: "2026-07-29T12:00:00Z"},
		{Name: "future", Expires: "2026-08-01T00:00:00Z"},
		{Name: "none"},
		{Name: "malformed", Expires: "next week"},
	}
	got := MarkExpired(in, asOf)
	want := map[string]bool{"past": true, "equal": true}
	for _, r := range got {
		if r.Expired != want[r.Name] {
			t.Errorf("%s: expired = %v", r.Name, r.Expired)
		}
	}
	if in[0].Expired || got[0].Owner == owner {
		t.Error("MarkExpired changed its input or shares an owner with it")
	}
	if MarkExpired(nil, asOf) != nil {
		t.Error("nil in, nil out")
	}
}
