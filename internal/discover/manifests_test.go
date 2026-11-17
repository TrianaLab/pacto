package discover

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const manifestsDir = "testdata/manifests"

func TestManifestContainerPortLines(t *testing.T) {
	r := readFixture(t, manifestsDir)
	for name, want := range map[string]string{
		"ports[9090]":  "9090/udp @ deploy/app.yaml:25",
		"ports[15000]": "15000/tcp @ deploy/app.yaml:20",
		"ports[7070]":  "7070/tcp @ deploy/app.yaml:50",
	} {
		if f := field(t, r.Fields, name); f.Value+" @ "+f.Source != want || f.Confidence != contract.ConfidenceLikely || f.Declare != "" {
			t.Errorf("%s = %+v, want %s", name, f, want)
		}
	}
	// The main container's image, one per workload; the sidecar's is not one.
	if img := field(t, r.Fields, "image"); img.Value != "ghcr.io/acme/app:1.0.0" || img.Source != "deploy/app.yaml:22" || !slices.Equal(img.Outranks, []string{"deploy/app.yaml:48"}) {
		t.Errorf("image = %+v", img)
	}
	if slices.ContainsFunc(r.entries, func(e entry) bool { return strings.Contains(e.Value, "envoy") || strings.Contains(e.Value, "http") }) {
		t.Errorf("a sidecar image or a named port was read: %+v", r.entries)
	}
	for name, want := range map[string]string{"configuration.DB_PASSWORD": "string, writeOnly @ deploy/app.yaml:29", "configuration.QUEUE_URL": "string @ deploy/app.yaml:31"} {
		if f := field(t, r.Fields, name); f.Value+" @ "+f.Source != want {
			t.Errorf("%s = %+v, want %s", name, f, want)
		}
	}
	rec, err := CompleteIn(manifestsDir, manifestsDir, nil, contract.InferredInputs{}, nil)
	if err != nil || rec == nil {
		t.Fatalf("record = %v, %v", rec, err)
	}
	if out := rec.Marshal(); bytes.Contains(out, []byte("hunter2")) {
		t.Errorf("an env value reached the record:\n%s", out)
	}
}

func TestManifestOutsideChartsOnly(t *testing.T) {
	r := readFixture(t, manifestsDir)
	if slices.ContainsFunc(r.entries, func(e entry) bool { return strings.HasPrefix(e.Source, "chart/") }) {
		t.Errorf("a manifest inside a chart was read: %+v", r.entries)
	}
	// A folder that is itself a chart reads no manifest at all.
	r = readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": "name: x\n", "crds/d.yaml": "apiVersion: apps/v1\nkind: Deployment\nspec: {template: {spec: {containers: [{name: a, ports: [{containerPort: 1}]}]}}}\n"}))
	if len(r.entries) != 0 {
		t.Errorf("entries = %+v", r.entries)
	}
}

func TestManifestShapes(t *testing.T) {
	for name, doc := range map[string]string{
		"no apiVersion":     "kind: Deployment\nspec: {template: {spec: {containers: [{name: a, image: x, ports: [{containerPort: 1}]}]}}}\n",
		"not a workload":    "apiVersion: v1\nkind: Pod\nspec: {containers: [{name: a, image: x, ports: [{containerPort: 1}]}]}\n",
		"no containers":     "apiVersion: apps/v1\nkind: Deployment\nspec: {}\n",
		"out of range port": "apiVersion: apps/v1\nkind: Deployment\nspec: {template: {spec: {containers: [{name: a, ports: [{containerPort: 70000}], env: [{value: v}]}]}}}\n",
		"a scalar document": "just text\n",
	} {
		r := readFixture(t, writeFiles(t, map[string]string{"m.yaml": doc}))
		if len(r.entries) != 0 {
			t.Errorf("%s: entries = %+v", name, r.entries)
		}
	}
}

func TestPortRankDockerfileOverManifest(t *testing.T) {
	r := readFixture(t, manifestsDir)
	f := field(t, r.Fields, "ports[8080]")
	if f.Source != "Dockerfile:2" || !slices.Equal(f.Outranks, []string{"deploy/app.yaml:24"}) {
		t.Errorf("ports[8080] = %+v", f)
	}
}
