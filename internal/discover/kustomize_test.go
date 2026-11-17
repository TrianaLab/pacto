package discover

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const kustomizeDir = "testdata/kustomize/payments"

func TestKustomizeResourcesRead(t *testing.T) {
	r := readFixture(t, kustomizeDir)
	// A local resource is read as a manifest; ../../base stays in the folder.
	if f := field(t, r.Fields, "ports[8080]"); f.Source != "base/deployment.yaml:12" {
		t.Errorf("ports[8080] = %+v", f)
	}
	got := findingsOf(r, CodeUnresolved)
	if len(got) != 1 || got[0].Source != "base/kustomization.yaml:6" || strings.Contains(got[0].Message, "shared") {
		t.Errorf("unresolved = %+v", got)
	}
}

func TestKustomizeRemoteResourceUnsupported(t *testing.T) {
	r := readFixture(t, kustomizeDir)
	var remote []contract.InferredFinding
	for _, f := range findingsOf(r, CodeUnsupportedFile) {
		if f.Source == "base/kustomization.yaml:5" {
			remote = append(remote, f)
		}
	}
	if len(remote) != 1 || !strings.Contains(remote[0].Message, "https://gitlab.example.com/org/base.git//deploy") {
		t.Fatalf("remote = %+v", remote)
	}
	for _, leak := range []string{"tok3n", "oauth2", "ref=x"} {
		if strings.Contains(remote[0].Message, leak) {
			t.Errorf("%q in %q", leak, remote[0].Message)
		}
	}
	for _, ref := range []string{"github.com/org/repo/deploy", "git@github.com:org/repo.git", "./x?ref=main"} {
		if !remoteResource(ref) {
			t.Errorf("%s is not remote", ref)
		}
	}
	for _, ref := range []string{"deployment.yaml", "../base", "sub/dir"} {
		if remoteResource(ref) {
			t.Errorf("%s is remote", ref)
		}
	}
}

func TestKustomizeImagesOverride(t *testing.T) {
	r := readFixture(t, kustomizeDir)
	f := field(t, r.Fields, "image")
	if f.Value != "ghcr.io/acme/payments:2.0.0" || f.Source != "overlays/prod/kustomization.yaml:6" || !slices.Equal(f.Outranks, []string{"base/deployment.yaml:10"}) {
		t.Errorf("image = %+v", f)
	}
	for name, files := range map[string]map[string]string{
		"only one, by digest": {"kustomization.yaml": "images:\n  - name: app\n    digest: sha256:abc\n"},
		"no new name":         {"kustomization.yaml": "images:\n  - name: app\n    newTag: \"3\"\n"},
	} {
		r := readFixture(t, writeFiles(t, files))
		want := map[string]string{"only one, by digest": "app@sha256:abc", "no new name": "app:3"}[name]
		if f := field(t, r.Fields, "image"); f.Value != want || f.Source != "kustomization.yaml:2" {
			t.Errorf("%s: image = %+v", name, f)
		}
	}
	// Two images, neither named like the service: none is read.
	r = readFixture(t, writeFiles(t, map[string]string{"kustomization.yaml": "images:\n  - name: a\n    newTag: \"1\"\n  - name: b\n    newTag: \"2\"\n"}))
	if len(r.entries) != 0 || len(findingsOf(r, CodeAmbiguous)) != 1 {
		t.Errorf("entries = %+v, findings = %+v", r.entries, r.Findings)
	}
}

func TestKustomizeHelmChartsUnsupported(t *testing.T) {
	r := readFixture(t, kustomizeDir)
	i := slices.IndexFunc(r.Findings, func(f contract.InferredFinding) bool { return f.Source == "overlays/charts/kustomization.yml:3" })
	if i < 0 || r.Findings[i].Code != CodeUnsupportedFile || !strings.Contains(r.Findings[i].Message, "helmCharts") {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestKustomizeShapes(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":            "",
		"a list":           "- a\n",
		"no images":        "resources: [a.yaml]\n",
		"an image no name": "images:\n  - newTag: \"1\"\n",
	} {
		r := readFixture(t, writeFiles(t, map[string]string{"kustomization.yaml": doc, "a.yaml": "x: 1\n"}))
		if len(r.entries) != 0 || len(r.Findings) != 0 {
			t.Errorf("%s: entries = %+v, findings = %+v", name, r.entries, r.Findings)
		}
	}
}
