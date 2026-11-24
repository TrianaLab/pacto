/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package xrdoffering

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// object reads one testdata object.
func object(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	j, err := sigsyaml.YAMLToJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(j); err != nil {
		t.Fatal(err)
	}
	return u
}

// compositions reads testdata/compositions.yaml.
func compositions(t *testing.T) []unstructured.Unstructured {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join("testdata", "compositions.yaml"))
	j, _ := sigsyaml.YAMLToJSON(data)
	var raw []map[string]any
	if err := json.Unmarshal(j, &raw); err != nil {
		t.Fatal(err)
	}
	out := make([]unstructured.Unstructured, len(raw))
	for i, r := range raw {
		b, _ := json.Marshal(r)
		if err := out[i].UnmarshalJSON(b); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func mapped(t *testing.T, xrd string) Offering {
	t.Helper()
	o, err := Map(object(t, xrd), compositions(t), "6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// params is o's provisioning schema.
func params(t *testing.T, o Offering) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(o.Files[SchemaFile], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func prop(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		m = m["properties"].(map[string]any)[p].(map[string]any)
	}
	return m
}

func TestMapperSlugV1ClaimKind(t *testing.T) {
	if o := mapped(t, "xrd-v1.yaml"); o.Slug != "bucket" || o.Generation != 3 {
		t.Fatalf("slug %q generation %d", o.Slug, o.Generation)
	}
}

func TestMapperSlugV2Kind(t *testing.T) {
	if o := mapped(t, "xrd-v2.yaml"); o.Slug != "xqueue" {
		t.Fatalf("slug %q", o.Slug)
	}
}

func TestMapperPicksReferenceableVersion(t *testing.T) {
	p := params(t, mapped(t, "xrd-v1.yaml"))
	if _, old := p["properties"].(map[string]any)["old"]; old {
		t.Fatal("the served, non-referenceable version was mapped")
	}
	if _, ok := p["properties"].(map[string]any)["storageGB"]; !ok {
		t.Fatal("the referenceable version was not mapped")
	}
	x := object(t, "xrd-v1.yaml")
	vs, _, _ := unstructured.NestedSlice(x.Object, "spec", "versions")
	vs[1].(map[string]any)["served"] = false
	_ = unstructured.SetNestedSlice(x.Object, vs, "spec", "versions")
	if _, err := Map(x, nil, "6.5.0"); err == nil || !strings.Contains(err.Error(), "served and referenceable") {
		t.Fatalf("err = %v", err)
	}
}

func TestMapperKeepsBoundsAndEnums(t *testing.T) {
	p := params(t, mapped(t, "xrd-v1.yaml"))
	if g := prop(p, "storageGB"); g["minimum"] != 1.0 || g["maximum"] != 500.0 {
		t.Errorf("storageGB = %v", g)
	}
	if n := prop(p, "name"); n["maxLength"] != 63.0 || n["pattern"] != "^[a-z0-9-]+$" {
		t.Errorf("name = %v", n)
	}
	if r := prop(p, "region"); !slices.Equal(r["enum"].([]any), []any{"eu-west-1", "us-east-1"}) {
		t.Errorf("region = %v", r)
	}
	if !slices.Contains(p["required"].([]any), any("region")) {
		t.Errorf("required = %v", p["required"])
	}
}

func TestMapperClosesEveryObject(t *testing.T) {
	p := params(t, mapped(t, "xrd-v1.yaml"))
	for _, n := range []map[string]any{p, prop(p, "lifecycle"), prop(p, "rules")["items"].(map[string]any)} {
		if n["additionalProperties"] != false {
			t.Errorf("open object %v", n)
		}
	}
	if _, ok := prop(p, "tags")["additionalProperties"].(map[string]any); !ok {
		t.Error("a typed map lost its value schema")
	}
}

func TestMapperDropsKubernetesKeywords(t *testing.T) {
	o := mapped(t, "xrd-v1.yaml")
	if bytes.Contains(o.Files[SchemaFile], []byte("x-kubernetes-")) {
		t.Fatalf("schema keeps a provider keyword:\n%s", o.Files[SchemaFile])
	}
	if !bytes.Contains(o.Files[ObjectFile], []byte("self == oldSelf")) {
		t.Error("the source object lost the XRD spec it was read from")
	}
}

func TestMapperPreserveUnknownStaysOpen(t *testing.T) {
	e := prop(params(t, mapped(t, "xrd-v1.yaml")), "extra")
	if _, typed := e["type"]; typed {
		t.Errorf("extra keeps a type: %v", e)
	}
	if _, closed := e["additionalProperties"]; closed {
		t.Errorf("extra was closed: %v", e)
	}
}

func TestMapperClassEnumFromSelectingCompositions(t *testing.T) {
	o := mapped(t, "xrd-v1.yaml")
	p := params(t, o)
	c := prop(p, "class")
	if c["type"] != "string" || !slices.Equal(c["enum"].([]any), []any{"large", "small"}) {
		t.Fatalf("class = %v", c)
	}
	if req := p["required"].([]any); !slices.Equal(req, []any{"region", "class"}) {
		t.Errorf("required = %v", req)
	}
	if !strings.Contains(string(o.Files[ClassesFile]), "name: large") || strings.Contains(string(o.Files[ClassesFile]), "other-group") {
		t.Errorf("classes = %s", o.Files[ClassesFile])
	}
	// A schema with no properties or required list still gets the class.
	x := object(t, "xrd-v2.yaml")
	vs, _, _ := unstructured.NestedSlice(x.Object, "spec", "versions")
	_ = unstructured.SetNestedField(vs[0].(map[string]any), map[string]any{"type": "object", "properties": map[string]any{"spec": map[string]any{"type": "object"}}},
		"schema", "openAPIV3Schema")
	_ = unstructured.SetNestedSlice(x.Object, vs, "spec", "versions")
	o, err := Map(x, compositions(t), "6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if p := params(t, o); prop(p, "class")["enum"].([]any)[0] != "queue-default" || p["required"].([]any)[0] != "class" {
		t.Errorf("schema = %v", p)
	}
}

func TestMapperNoCompositionFinding(t *testing.T) {
	o, err := Map(object(t, "xrd-v1.yaml"), nil, "6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := params(t, o)["properties"].(map[string]any)["class"]; ok {
		t.Error("a class property with no Composition")
	}
	rec, err := contract.ParseInferredRecord(o.Files[contract.InferredRecordFile])
	if err != nil || len(rec.Findings) != 1 || rec.Findings[0].Code != CodeNoClass {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

func TestMapperNoSpecSchemaIsAClosedObject(t *testing.T) {
	x := object(t, "xrd-v2.yaml")
	vs, _, _ := unstructured.NestedSlice(x.Object, "spec", "versions")
	_ = unstructured.SetNestedField(vs[0].(map[string]any), map[string]any{"type": "object"}, "schema", "openAPIV3Schema")
	_ = unstructured.SetNestedSlice(x.Object, vs, "spec", "versions")
	o, err := Map(x, nil, "6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if p := params(t, o); p["type"] != "object" || p["additionalProperties"] != false {
		t.Errorf("schema = %v", p)
	}
}

func TestMapperSummary(t *testing.T) {
	var pf struct {
		Service struct{ Summary string }
	}
	_ = yaml.Unmarshal(mapped(t, "xrd-v1.yaml").Files["pacto.yaml"], &pf)
	if pf.Service.Summary != "An object storage bucket." {
		t.Errorf("summary = %q", pf.Service.Summary)
	}
	_ = yaml.Unmarshal(mapped(t, "xrd-v2.yaml").Files["pacto.yaml"], &pf)
	if pf.Service.Summary != "Inferred from XRD xqueues.messaging.acme.io" {
		t.Errorf("summary = %q", pf.Service.Summary)
	}
	x := object(t, "xrd-v2.yaml")
	vs, _, _ := unstructured.NestedSlice(x.Object, "spec", "versions")
	_ = unstructured.SetNestedField(vs[0].(map[string]any), "a\tb"+strings.Repeat("x", 300), "schema", "openAPIV3Schema", "description")
	_ = unstructured.SetNestedSlice(x.Object, vs, "spec", "versions")
	o, _ := Map(x, nil, "6.5.0")
	_ = yaml.Unmarshal(o.Files["pacto.yaml"], &pf)
	if len([]rune(pf.Service.Summary)) != maxSummary || !strings.HasPrefix(pf.Service.Summary, "a b") {
		t.Errorf("summary = %q", pf.Service.Summary)
	}
}

// bundle is o's files as a bundle.
func bundle(t *testing.T, o Offering) *contract.Bundle {
	t.Helper()
	fsys := fstest.MapFS{}
	for k, v := range o.Files {
		fsys[k] = &fstest.MapFile{Data: v}
	}
	c, err := contract.Parse(bytes.NewReader(o.Files["pacto.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	if res := validation.Validate(c, o.Files["pacto.yaml"], fsys); !res.IsValid() {
		t.Fatalf("invalid contract: %+v", res.Errors)
	}
	return &contract.Bundle{Contract: c, RawYAML: o.Files["pacto.yaml"], FS: fsys}
}

// assertContribution maps the v1 fixture with comps and checks the bundle is
// an offering contribution whose record verifies and cites its sources.
func assertContribution(t *testing.T, comps []unstructured.Unstructured) {
	t.Helper()
	o, err := Map(object(t, "xrd-v1.yaml"), comps, "6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	b := bundle(t, o)
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.VerifyInferredRecord(b.FS, rec); err != nil {
		t.Fatalf("record: %v", err)
	}
	if why := contract.ContributionRefusal(b, rec); why != "" {
		t.Fatalf("refused: %s", why)
	}
	if k := oci.BundleKind(b.Contract); k != oci.KindOffering {
		t.Errorf("kind = %s", k)
	}
	sources := make([]string, 0, len(rec.Fields))
	for _, f := range rec.Fields {
		sources = append(sources, f.Source)
	}
	if !slices.Contains(sources, ObjectFile) || rec.Inputs.Pacto != "6.5.0" || rec.Inputs.Discovery != Discovery {
		t.Errorf("record = %+v", rec)
	}
	if comps != nil && !slices.Contains(sources, ClassesFile) {
		t.Errorf("no field cites %s", ClassesFile)
	}
}

func TestMapperBundleIsAContribution(t *testing.T) {
	for _, comps := range [][]unstructured.Unstructured{compositions(t), nil} {
		assertContribution(t, comps)
	}
	if o := mapped(t, "xrd-v1.yaml"); bytes.Contains(o.Files[ObjectFile], []byte("managedFields")) ||
		bytes.Contains(o.Files[ObjectFile], []byte("Established")) || bytes.Contains(o.Files[ObjectFile], []byte("uid")) {
		t.Errorf("the source object keeps cluster state:\n%s", o.Files[ObjectFile])
	}
}

func TestMapperSkipsNonDNSSlug(t *testing.T) {
	x := object(t, "xrd-v2.yaml")
	_ = unstructured.SetNestedField(x.Object, "X_Queue", "spec", "names", "kind")
	if _, err := Map(x, nil, "6.5.0"); err == nil || !strings.Contains(err.Error(), "no DNS label") {
		t.Fatalf("err = %v", err)
	}
}

func TestMapperVersionCarriesGeneration(t *testing.T) {
	o := mapped(t, "xrd-v1.yaml")
	if c := bundle(t, o).Contract; c.Service.Version != "0.0.0-inferred.3.0" || c.Service.Offering != "bucket" || c.Service.Name != "bucket" {
		t.Fatalf("service = %+v", c.Service)
	}
	if c := bundle(t, o.At(Version(3, 1))).Contract; c.Service.Version != "0.0.0-inferred.3.1" {
		t.Fatalf("version = %s", c.Service.Version)
	}
	if o.Files["pacto.yaml"] == nil || bundle(t, o).Contract.Service.Version != "0.0.0-inferred.3.0" {
		t.Error("At changed the offering it was called on")
	}
}

func TestMapperSameContentSameDigest(t *testing.T) {
	a, b := mapped(t, "xrd-v1.yaml"), mapped(t, "xrd-v1.yaml")
	da, _ := oci.BundleDigests(bundle(t, a))
	db, _ := oci.BundleDigests(bundle(t, b))
	if da.Typed != db.Typed {
		t.Fatalf("digests %s and %s", da.Typed, db.Typed)
	}
}
