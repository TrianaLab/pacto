/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package xrdoffering maps one Crossplane CompositeResourceDefinition (XRD)
// and the Compositions that select it to the files of an inferred offering:
// a contribution bundle whose pacto.yaml declares service.offering, a closed
// provisioning configuration schema that keeps the XRD's bounds and enums and
// names the Compositions as the class enum, and the record citing the source
// files it was read from. It is pure: unstructured objects in, files out.
package xrdoffering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// The bundle files an inferred offering holds, part of the public format.
const (
	SchemaFile  = "configuration/provisioning.schema.json"
	ObjectFile  = "source/object.yaml"
	ClassesFile = "source/classes.yaml"
)

// Discovery is the mapper version a record names.
const Discovery = "xrdoffering/1"

// CodeNoClass is the record finding of an XRD no Composition selects: the
// offering has no class to provision with.
const CodeNoClass = "NO_CLASS"

// pactoFileName is the contract file of every bundle.
const pactoFileName = "pacto.yaml"

// maxSummary bounds service.summary (the schema's maxLength).
const maxSummary = 200

// Offering is one XRD mapped: its slug (service.name and service.offering),
// its generation and its bundle files at version Version(Generation, 0).
type Offering struct {
	Slug       string
	Generation int64
	Files      map[string][]byte
	summary    string
}

// Version is the version an inferred offering carries: the generation and a
// count of content changes within it, as numeric prerelease identifiers so
// semver orders them (build metadata would not be ordered, and an OCI tag
// holds no '+').
func Version(generation, k int64) string {
	return "0.0.0-inferred." + strconv.FormatInt(generation, 10) + "." + strconv.FormatInt(k, 10)
}

// Map maps xrd and the compositions that select it, recording pacto as the
// Pacto that mapped it. An XRD whose slug is no DNS label or that serves no
// referenceable version is an error: it is skipped and counted.
func Map(xrd *unstructured.Unstructured, compositions []unstructured.Unstructured, pacto string) (Offering, error) {
	slug := slugOf(xrd)
	if !contract.ValidServiceName(slug) {
		return Offering{}, fmt.Errorf("XRD %s: slug %q is no DNS label", xrd.GetName(), slug)
	}
	root, err := pickVersion(xrd)
	if err != nil {
		return Offering{}, fmt.Errorf("XRD %s: %w", xrd.GetName(), err)
	}
	spec, _ := root["properties"].(map[string]any)
	params := mapSchema(spec["spec"])
	cls := classes(xrd, compositions)
	if len(cls) > 0 {
		names := make([]any, len(cls))
		for i, c := range cls {
			names[i] = c.Name
		}
		props, _ := params["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
			params["properties"] = props
		}
		// ponytail: an XRD that declares its own class field has it replaced;
		// the class is how a Composition is selected.
		props["class"] = map[string]any{"type": "string", "enum": names}
		req, _ := params["required"].([]any)
		if !slices.Contains(req, any("class")) {
			params["required"] = append(req, "class")
		}
	}
	o := Offering{Slug: slug, Generation: xrd.GetGeneration(), summary: summaryOf(xrd, root)}
	o.Files = bundleFiles(o, xrd, params, cls, pacto)
	return o, nil
}

// At is o with its pacto.yaml at version.
func (o Offering) At(version string) Offering {
	files := maps.Clone(o.Files) // Map always sets Files
	files[pactoFileName] = pactoYAML(o, version)
	o.Files = files
	return o
}

// slugOf is the lowercased claim kind when the XRD names one, else its kind,
// so the slug stays the same whichever API version lists the XRD.
func slugOf(xrd *unstructured.Unstructured) string {
	kind, _, _ := unstructured.NestedString(xrd.Object, "spec", "claimNames", "kind")
	if kind == "" {
		kind, _, _ = unstructured.NestedString(xrd.Object, "spec", "names", "kind")
	}
	return strings.ToLower(kind)
}

// pickVersion is the openAPIV3Schema of the XRD version that is served and
// referenceable.
func pickVersion(xrd *unstructured.Unstructured) (map[string]any, error) {
	versions, _, _ := unstructured.NestedSlice(xrd.Object, "spec", "versions")
	for _, v := range versions {
		m, _ := v.(map[string]any)
		served, _, _ := unstructured.NestedBool(m, "served")
		ref, _, _ := unstructured.NestedBool(m, "referenceable")
		if served && ref {
			s, _, _ := unstructured.NestedMap(m, "schema", "openAPIV3Schema")
			return s, nil
		}
	}
	return nil, errors.New("no version is both served and referenceable")
}

// mapSchema is a closed copy of the XRD's spec schema: every x-kubernetes-*
// keyword is dropped once read (provider hints stay out of the contract), a
// node that preserves unknown fields keeps no type, so acceptance names it
// open, and every typed object is closed.
//
// ponytail: x-pacto-tier is not written yet; untiered reads as contract, the
// tier every mapped field gets. The keyword lands with the shared tier reader.
func mapSchema(spec any) map[string]any {
	params, _ := runtime.DeepCopyJSONValue(spec).(map[string]any)
	if params == nil {
		params = map[string]any{"type": "object"}
	}
	schemax.Walk(params, func(m map[string]any) {
		if keep, _ := m["x-kubernetes-preserve-unknown-fields"].(bool); keep {
			delete(m, "type")
		}
		for k := range m {
			if strings.HasPrefix(k, "x-kubernetes-") {
				delete(m, k)
			}
		}
	})
	schemax.Close(params)
	return params
}

// class is one Composition that selects the XRD.
type class struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Name       string `yaml:"name"`
}

// classes are the Compositions whose compositeTypeRef names the XRD's group
// and kind, at any version, sorted by name.
func classes(xrd *unstructured.Unstructured, compositions []unstructured.Unstructured) []class {
	group, _, _ := unstructured.NestedString(xrd.Object, "spec", "group")
	kind, _, _ := unstructured.NestedString(xrd.Object, "spec", "names", "kind")
	out := []class{}
	for _, c := range compositions {
		apiVersion, _, _ := unstructured.NestedString(c.Object, "spec", "compositeTypeRef", "apiVersion")
		refKind, _, _ := unstructured.NestedString(c.Object, "spec", "compositeTypeRef", "kind")
		gv, err := schema.ParseGroupVersion(apiVersion)
		if err == nil && gv.Group == group && refKind == kind {
			out = append(out, class{APIVersion: c.GetAPIVersion(), Kind: c.GetKind(), Name: c.GetName()})
		}
	}
	slices.SortFunc(out, func(a, b class) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// summaryOf is the first line of the version schema's description, made one
// printable line of at most maxSummary characters, else a fixed line naming
// the XRD.
func summaryOf(xrd *unstructured.Unstructured, root map[string]any) string {
	d, _ := root["description"].(string)
	line, _, _ := strings.Cut(strings.TrimSpace(d), "\n")
	line = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, line))
	if line == "" {
		line = "Inferred from XRD " + xrd.GetName()
	}
	if r := []rune(line); len(r) > maxSummary {
		line = string(r[:maxSummary])
	}
	return line
}

// pactoFile is pacto.yaml, in its key order.
type pactoFile struct {
	PactoVersion   string            `yaml:"pactoVersion"`
	Service        pactoService      `yaml:"service"`
	Configurations []pactoConfigFile `yaml:"configurations"`
}

type pactoService struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	Offering string `yaml:"offering"`
	Summary  string `yaml:"summary"`
}

type pactoConfigFile struct {
	Name     string `yaml:"name"`
	Schema   string `yaml:"schema"`
	Required bool   `yaml:"required"`
}

// pactoYAML is o's pacto.yaml at version.
func pactoYAML(o Offering, version string) []byte {
	b, _ := yaml.Marshal(pactoFile{PactoVersion: "2.5", // a struct of strings always marshals
		Service:        pactoService{Name: o.Slug, Version: version, Offering: o.Slug, Summary: o.summary},
		Configurations: []pactoConfigFile{{Name: "provisioning", Schema: SchemaFile, Required: true}}})
	return b
}

// bundleFiles are the bundle files of o: pacto.yaml at generation and k 0,
// the schema, the source object and classes and the record citing them. Map
// keys marshal sorted, so the same input yields the same bytes.
func bundleFiles(o Offering, xrd *unstructured.Unstructured, params map[string]any, cls []class, pacto string) map[string][]byte {
	// Unstructured content is JSON values, which always marshal.
	schemaJSON, _ := json.MarshalIndent(params, "", "  ")
	spec, _, _ := unstructured.NestedMap(xrd.Object, "spec")
	object, _ := yaml.Marshal(map[string]any{"apiVersion": xrd.GetAPIVersion(), "kind": xrd.GetKind(),
		"metadata": map[string]any{"name": xrd.GetName(), "generation": xrd.GetGeneration()}, "spec": spec})
	classesYAML, _ := yaml.Marshal(map[string]any{"compositions": cls})
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: pacto, Discovery: Discovery, Sources: []contract.InferredSourceFile{
			{Path: ClassesFile, Digest: digest(classesYAML)}, {Path: ObjectFile, Digest: digest(object)}}},
		Fields: []contract.Inferred{
			{Field: "service.offering", Value: o.Slug, Source: ObjectFile, Confidence: contract.ConfidenceLikely},
			{Field: "configurations[provisioning]", Value: SchemaFile, Source: ObjectFile, Confidence: contract.ConfidenceLikely},
		}}
	if len(cls) == 0 {
		rec.Findings = []contract.InferredFinding{{Code: CodeNoClass, Source: ClassesFile, Message: "no Composition selects this XRD"}}
	} else {
		names := make([]string, len(cls))
		for i, c := range cls {
			names[i] = c.Name
		}
		rec.Fields = append(rec.Fields, contract.Inferred{Field: "configurations[provisioning].class", Value: strings.Join(names, ", "),
			Source: ClassesFile, Confidence: contract.ConfidenceLikely})
	}
	files := map[string][]byte{SchemaFile: append(schemaJSON, '\n'), ObjectFile: object, ClassesFile: classesYAML,
		contract.InferredRecordFile: rec.Marshal()}
	return Offering{Slug: o.Slug, summary: o.summary, Files: files}.At(Version(o.Generation, 0)).Files
}

// digest is data's sha256 as a record source digest.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
