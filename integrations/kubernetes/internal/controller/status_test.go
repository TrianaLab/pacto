/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func TestPopulateContractStatus_RefPropertiesFromTheResolvedSchema(t *testing.T) {
	c := &contract.Contract{Configurations: []contract.Configuration{
		{Name: "db", Ref: providerRef},
		{Name: "cache", Ref: "oci://ghcr.io/acme/redis:7.0.0"},
		{Name: "tuned", Ref: providerRef, Values: map[string]any{"size": "large"}},
	}}
	provider := fstest.MapFS{"configuration/schema.json": &fstest.MapFile{Data: []byte(providerSchema)}}
	refs := validation.ConfigRefs{
		"db":    {Name: "db", Schema: compiledSchema(t), SchemaPath: "configuration/schema.json", ProviderFS: provider},
		"cache": {Name: "cache"}, // unresolved
		"tuned": {Name: "tuned", Schema: compiledSchema(t), SchemaPath: "configuration/schema.json", ProviderFS: provider},
	}
	p := &pactov1alpha1.Pacto{}
	newReconciler().populateContractStatus(p, &loader.LoadResult{Contract: c}, refs)

	keys := func(i int) []string {
		props := p.Status.Configurations[i].Properties
		out := make([]string, 0, len(props))
		for _, sp := range props {
			out = append(out, sp.Key)
		}
		return out
	}
	if got := keys(0); len(got) != 2 {
		t.Errorf("db properties = %v, want the provider schema's size and version", got)
	}
	if got := keys(1); len(got) != 0 {
		t.Errorf("an unresolved ref reports properties %v, want none", got)
	}
	if got := keys(2); len(got) != 1 || got[0] != "size" {
		t.Errorf("a ref with values reports %v, want its values' keys", got)
	}
}

// compiledSchema is the provider schema compiled, as a resolved ref carries it.
func compiledSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(providerSchema))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:///s.json", doc); err != nil {
		t.Fatal(err)
	}
	return c.MustCompile("mem:///s.json")
}

func TestPopulateContractStatus_MetadataJSON(t *testing.T) {
	c := &contract.Contract{Metadata: map[string]any{
		"team":   "payments",
		"labels": map[string]any{"tier": "gold"},
		"zones":  []any{"a", "b"},
		"count":  3,
		"bad":    math.Inf(1), // JSON has no infinity
	}}
	p := &pactov1alpha1.Pacto{}
	newReconciler().populateContractStatus(p, &loader.LoadResult{Contract: c}, nil)

	want := map[string]string{
		"team":   "payments",
		"labels": `{"tier":"gold"}`,
		"zones":  `["a","b"]`,
		"count":  "3",
		"bad":    "+Inf",
	}
	for k, v := range want {
		if got := p.Status.Metadata[k]; got != v {
			t.Errorf("metadata[%s] = %q, want %q", k, got, v)
		}
	}
}
