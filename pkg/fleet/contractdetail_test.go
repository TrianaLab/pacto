package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const policySchemaJSON = `{"title":"T","description":"D","properties":{"a":{"type":"string"},"b":{"type":"object","properties":{"c":{"type":"integer"}}}}}`

func schemaRev(files fstest.MapFS) *ContractRevision {
	return &ContractRevision{Key: "svc@1", bundle: &contract.Bundle{FS: files}}
}

func wideSchema(n int) string {
	props := make([]string, n)
	for i := range props {
		props[i] = fmt.Sprintf(`"p%03d":{"type":"string"}`, i)
	}
	return `{"properties":{` + strings.Join(props, ",") + `}}`
}

func schemaFiles() *ContractRevision {
	return schemaRev(fstest.MapFS{
		"policy/schema.json": {Data: []byte(policySchemaJSON)},
		"broken.json":        {Data: []byte(`{"properties":`)},
		"wide.json":          {Data: []byte(wideSchema(250))},
		"long.json":          {Data: []byte(`{"title":"` + strings.Repeat("x", 300) + `","properties":{"k":{"type":"string","default":"` + strings.Repeat("v", 300) + `"}}}`)},
		"empty.json":         {Data: []byte(`{"type":"object"}`)},
		"huge.json":          {Data: make([]byte, maxSchemaBytes+1)},
	})
}

func TestRevisionSchema(t *testing.T) {
	rev := schemaFiles()
	got := revisionSchema(rev, "policy/schema.json")
	if got.Title != "T" || got.Description != "D" || got.Total != 2 || got.Truncated || got.Unavailable != "" {
		t.Fatalf("content = %+v", got)
	}
	if got.Properties[0].Key != "a" || got.Properties[1].Key != "b.c" || got.Properties[1].Type != "integer" {
		t.Errorf("properties = %+v", got.Properties)
	}
}

func TestRevisionSchemaBounds(t *testing.T) {
	rev := schemaFiles()
	wide := revisionSchema(rev, "wide.json")
	if !wide.Truncated || len(wide.Properties) != MaxDetailPreview || wide.Total != 250 {
		t.Errorf("wide: truncated = %v, len = %d, total = %d", wide.Truncated, len(wide.Properties), wide.Total)
	}
	long := revisionSchema(rev, "long.json")
	if want := strings.Repeat("x", maxRuntimeValueLen) + "…"; long.Title != want || long.Properties[0].Value != strings.Repeat("v", maxRuntimeValueLen)+"…" {
		t.Errorf("long values not capped: title %d, value %d", len(long.Title), len(long.Properties[0].Value))
	}
	empty := revisionSchema(rev, "empty.json")
	if empty.Properties == nil || len(empty.Properties) != 0 || empty.Unavailable != "" || empty.Total != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

func TestRevisionSchemaUnavailable(t *testing.T) {
	rev := schemaFiles()
	unavailable := map[string]string{
		"missing.json": "could not be read",
		"policy":       "could not be read", // a directory: opens, then fails to read
		"broken.json":  "could not be parsed",
		"huge.json":    "exceeds the 1048576-byte read bound",
	}
	for path, want := range unavailable {
		c := revisionSchema(rev, path)
		if !strings.Contains(c.Unavailable, want) || c.Properties == nil || len(c.Properties) != 0 {
			t.Errorf("%s: content = %+v, want unavailable %q", path, c, want)
		}
	}

	for name, r := range map[string]*ContractRevision{
		"nil bundle": {Key: "svc@1"},
		"nil FS":     {Key: "svc@1", bundle: &contract.Bundle{}},
	} {
		if c := revisionSchema(r, "policy/schema.json"); !strings.Contains(c.Unavailable, "not retained") {
			t.Errorf("%s: unavailable = %q", name, c.Unavailable)
		}
	}
	conflict := schemaRev(fstest.MapFS{"policy/schema.json": {Data: []byte(policySchemaJSON)}})
	conflict.docConflict = "sources disagree"
	if c := revisionSchema(conflict, "policy/schema.json"); c.Unavailable != "sources disagree" {
		t.Errorf("conflict: unavailable = %q", c.Unavailable)
	}
}

func TestWithSchemaContent(t *testing.T) {
	rev := schemaRev(fstest.MapFS{"policy/schema.json": {Data: []byte(policySchemaJSON)}, "cfg/schema.json": {Data: []byte(policySchemaJSON)}})
	res := &RefResolution{Resolved: true, Service: &EntityRef{Kind: KindService, Key: "shared"}}
	shared := ConfigurationsPreview{Total: 2, Count: 2, Items: []ConfigurationSummary{{Name: "app", Schema: "cfg/schema.json"}, {Name: "vals"}}}
	pols := PoliciesPreview{Total: 2, Count: 2, Items: []PolicySummary{{Name: "local", Schema: "policy/schema.json"}, {Name: "remote", Ref: "oci://x/p", Resolution: res}}}

	cfgs, gotPols := withSchemaContent(&FleetSnapshot{}, rev, shared, pols)
	if cfgs.Items[0].Content == nil || cfgs.Items[0].Content.Total != 2 || cfgs.Items[1].Content != nil {
		t.Errorf("configurations = %+v", cfgs.Items)
	}
	if gotPols.Items[0].Content == nil || gotPols.Items[1].Content != nil || gotPols.Items[1].Resolution != res {
		t.Errorf("policies = %+v", gotPols.Items)
	}
	if shared.Items[0].Content != nil || pols.Items[0].Content != nil {
		t.Error("withSchemaContent mutated the shared projection")
	}
}

const schemaContractYAML = `pactoVersion: '2.0'
service:
  name: svc
  version: %s
workload: service
configurations:
- name: app
  schema: configuration/schema.json
  required: true
- name: vals
  values:
    a: 1
  required: false
policies:
%s
`

func schemaBundle(t *testing.T, version string, policies ...string) *contract.Bundle {
	t.Helper()
	files := fstest.MapFS{"configuration/schema.json": {Data: []byte(policySchemaJSON)}}
	var pols []string
	for _, p := range policies {
		path := "policy/" + p + ".json"
		files[path] = &fstest.MapFile{Data: []byte(policySchemaJSON)}
		pols = append(pols, "- name: "+p+"\n  schema: "+path)
	}
	y := fmt.Sprintf(schemaContractYAML, version, strings.Join(pols, "\n"))
	return &contract.Bundle{Contract: mustParse(t, y), RawYAML: []byte(y), FS: files}
}

func schemaFleet(t *testing.T, revs []RawRevision, targets []RawTarget) *Query {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("l", "local", &Collection{Revisions: revs, Targets: targets}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestRevisionDetailSchemaContent(t *testing.T) {
	q := schemaFleet(t, []RawRevision{{Bundle: schemaBundle(t, "1.0.0", "p1"), Digest: "sha256:one"}}, nil)
	d, err := q.EntityDetail(KindRevision, string(revKeyForVersion(t, q, "svc", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Revision.Configurations.Items[0].Content; c == nil || c.Total != 2 {
		t.Errorf("configuration content = %+v", c)
	}
	if d.Revision.Configurations.Items[1].Content != nil {
		t.Error("values-only configuration has content")
	}
	if c := d.Revision.Policies.Items[0].Content; c == nil || c.Title != "T" {
		t.Errorf("policy content = %+v", c)
	}
}

// builtRev is a hand-built revision carrying what Build would derive from b.
func builtRev(key RevisionKey, version string, b *contract.Bundle) *ContractRevision {
	return &ContractRevision{Key: key, Version: version, Contract: b.Contract, bundle: b, configurations: configurationsPreview(b.Contract.Configurations)}
}

func TestServiceDetailPolicies(t *testing.T) {
	q := detailFleet(contract.Owner{}, []*ContractRevision{
		builtRev("svc@1", "1.0.0", schemaBundle(t, "1.0.0", "p1")),
		builtRev("svc@2", "2.0.0", schemaBundle(t, "2.0.0", "p1", "p2")),
		{Key: "svc@3", Version: "3.0.0"},
	}, []*TargetRecord{{Key: "t1", Name: "t1", ContractRevision: "svc@2"}})
	got := serviceDetailOf(t, q)
	if got.CurrentRevision == nil || got.CurrentRevision.Key != "svc@2" {
		t.Fatalf("current revision = %+v", got.CurrentRevision)
	}
	if got.Policies.Total != 2 || got.Policies.Items[1].Content == nil || got.Policies.Items[1].Content.Total != 2 {
		t.Errorf("policies = %+v", got.Policies)
	}
	if got.Configurations.Total != 2 || got.Configurations.Items[0].Content == nil {
		t.Errorf("configurations = %+v", got.Configurations)
	}
}

// TestServiceDetailPoliciesFallback: with no target the newest known revision is
// current (here one with no readable contract), and with no revision there is none.
func TestServiceDetailPoliciesFallback(t *testing.T) {
	q := detailFleet(contract.Owner{}, []*ContractRevision{
		{Key: "svc@1", Version: "1.0.0", Contract: schemaBundle(t, "1.0.0", "p1").Contract},
		{Key: "svc@3", Version: "3.0.0"},
	}, nil)
	got := serviceDetailOf(t, q)
	if got.CurrentRevision == nil || got.CurrentRevision.Key != "svc@3" || got.Policies.Items == nil || got.Policies.Total != 0 || got.Configurations.Items == nil {
		t.Errorf("no target: current = %+v, policies = %+v", got.CurrentRevision, got.Policies)
	}

	got = serviceDetailOf(t, detailFleet(contract.Owner{}, nil, nil))
	if got.CurrentRevision != nil || got.Policies.Items == nil || got.Configurations.Items == nil || got.Policies.Total != 0 {
		t.Errorf("no revisions: %+v %+v %+v", got.CurrentRevision, got.Policies, got.Configurations)
	}
}

func TestServiceDetailCatalogRevisionNoFS(t *testing.T) {
	b := schemaBundle(t, "1.0.0", "p1")
	b.FS = nil
	q := schemaFleet(t, []RawRevision{{Bundle: b, Digest: "sha256:one"}}, nil)
	d, err := q.EntityDetail(KindService, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Service.Policies.Items[0].Content; c == nil || !strings.Contains(c.Unavailable, "not retained") {
		t.Errorf("policy content = %+v", c)
	}
}

func TestConfigurationSummaryRefContent(t *testing.T) {
	pg := "oci://r/pg@" + refDigest("pg")
	a := "oci://r/a@" + refDigest("a")
	content := func(t *testing.T, revs ...RawRevision) ConfigurationSummary {
		t.Helper()
		snap := refSnap(t, revs...)
		d, err := NewQuery(snap).EntityDetail(KindRevision, string(revisionOf(t, snap, "orders").Key))
		if err != nil {
			t.Fatal(err)
		}
		return d.Revision.Configurations.Items[0]
	}
	selected := refBundle(t, "orders", fmt.Sprintf("ref: %q, configuration: db", pg), fstest.MapFS{})
	if got := content(t, pgProvider(t), selected); got.Configuration != "db" || got.Content == nil || got.Content.Total != 1 || got.Content.Properties[0].Key != "size" {
		t.Errorf("resolved = %+v", got)
	}
	if got := content(t, pgProvider(t), refOrders(t, "oci://r/pg:1.0.0", "small", "")); got.Content != nil {
		t.Errorf("unresolved has content: %+v", got.Content)
	}
	chain := refBundle(t, "a", fmt.Sprintf("ref: %q", pg), fstest.MapFS{})
	if got := content(t, pgProvider(t), chain, refOrders(t, a, "small", "")); got.Content == nil || got.Content.Total != 1 {
		t.Errorf("chained = %+v", got)
	}
	none := map[string]RawRevision{
		"selection fails": refBundle(t, "a", "schema: db.json", fstest.MapFS{"db.json": {Data: []byte(refSchemaJSON)}}),
		"values only":     refBundle(t, "a", "values: {size: small}", fstest.MapFS{}),
		"hop unresolved":  refBundle(t, "a", `ref: "oci://r/gone:1"`, fstest.MapFS{}),
	}
	for name, provider := range none {
		orders := refOrders(t, a, "small", "")
		if name == "selection fails" {
			orders = refBundle(t, "orders", fmt.Sprintf("ref: %q, configuration: other", a), fstest.MapFS{})
		}
		if got := content(t, provider, orders); got.Resolution == nil || !got.Resolution.Resolved || got.Content != nil {
			t.Errorf("%s = %+v", name, got)
		}
	}
	cycleB := refBundle(t, "b", fmt.Sprintf("ref: %q", a), fstest.MapFS{})
	cycleA := refBundle(t, "a", fmt.Sprintf("ref: %q", "oci://r/b@"+refDigest("b")), fstest.MapFS{})
	if got := content(t, cycleA, cycleB, refOrders(t, a, "small", "")); got.Content != nil {
		t.Errorf("cycle has content: %+v", got.Content)
	}
}
