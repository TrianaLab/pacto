package delivery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

var updateInstall = flag.Bool("update-install", false, "rewrite testdata/memberinstall-*.golden.yaml")

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fluxCand is a Flux candidate naming the kubeconfig kind ns/name.
func fluxCand(kind, ns, name string) detect.Candidate {
	return detect.Candidate{Source: detect.SourceFlux, Kind: kind, Namespace: ns, Name: name, KubeConfig: &detect.Object{Kind: kind, Namespace: ns, Name: name}}
}

func installOpts(engine string) MemberInstallOptions {
	return MemberInstallOptions{Engine: engine, Registry: "ghcr.io/acme", ChartVersion: "4.6.0",
		Flux: []detect.Candidate{fluxCand("Secret", "flux-system", "eu-prod"), fluxCand("ConfigMap", "clusters", "us-east")}}
}

func mustInstall(t *testing.T, o MemberInstallOptions) MemberInstallResult {
	t.Helper()
	res, err := MemberInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// docs are every YAML document of res, decoded.
func docs(t *testing.T, res MemberInstallResult) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(MemberInstallYAML(res.Files)))
	for {
		var m map[string]any
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
}

// at walks m along path.
func at(m any, path ...string) any {
	for _, p := range path {
		mm, _ := m.(map[string]any)
		m = mm[p]
	}
	return m
}

// values are every chart values object res sets.
func values(t *testing.T, res MemberInstallResult) []any {
	t.Helper()
	var out []any
	for _, d := range docs(t, res) {
		switch d["kind"] {
		case "ApplicationSet":
			out = append(out, at(d, "spec", "template", "spec", "source", "helm", "valuesObject"))
		case "HelmRelease":
			out = append(out, at(d, "spec", "values"))
		}
	}
	return out
}

func TestMemberInstallValuesAreRegistryAndHubURL(t *testing.T) {
	for _, engine := range []string{InstallArgoCD, InstallFlux} {
		vs := values(t, mustInstall(t, installOpts(engine)))
		want := map[string]any{"registry": "ghcr.io/acme"}
		if len(vs) == 0 {
			t.Fatalf("%s: no values", engine)
		}
		for _, v := range vs {
			if !reflect.DeepEqual(v, want) {
				t.Fatalf("%s: values %v", engine, v)
			}
		}
		o := installOpts(engine)
		o.HubURL = "wss://hub.acme.example:8443/live"
		want["hub"] = map[string]any{"url": "wss://hub.acme.example:8443/live"}
		for _, v := range values(t, mustInstall(t, o)) {
			if !reflect.DeepEqual(v, want) {
				t.Fatalf("%s: values %v", engine, v)
			}
		}
	}
}

func TestMemberInstallInsecureRegistryAddsOnlyThat(t *testing.T) {
	for _, engine := range []string{InstallArgoCD, InstallFlux} {
		o := installOpts(engine)
		o.Registry, o.InsecureRegistry, o.Digest = "zot.local:5000/acme", true, testDigest
		res := mustInstall(t, o)
		for _, v := range values(t, res) {
			if !reflect.DeepEqual(v, map[string]any{"registry": "zot.local:5000/acme", "insecureRegistries": []any{"zot.local:5000"}}) {
				t.Fatalf("%s: values %v", engine, v)
			}
		}
		o.InsecureRegistry = false
		plain := docs(t, mustInstall(t, o))
		for i, d := range docs(t, res) {
			switch d["kind"] {
			case "Secret":
				if at(d, "stringData", "insecureOCIForceHttp") != "true" || at(plain[i], "stringData", "insecureOCIForceHttp") != nil {
					t.Fatalf("secret %v", d)
				}
			case "OCIRepository":
				if at(d, "spec", "insecure") != true || at(plain[i], "spec", "insecure") != nil {
					t.Fatalf("repository %v", d)
				}
			}
		}
	}
}

func TestArgoPostSelectorExcludesInClusterSecret(t *testing.T) {
	d := docs(t, mustInstall(t, installOpts(InstallArgoCD)))[0]
	gen := at(d, "spec", "generators").([]any)[0]
	post := at(gen, "selector", "matchExpressions").([]any)[0]
	if !reflect.DeepEqual(post, map[string]any{"key": "server", "operator": "NotIn", "values": []any{"https://kubernetes.default.svc"}}) {
		t.Fatalf("post selector %v", post)
	}
	optOut := at(gen, "clusters", "selector", "matchExpressions").([]any)[0]
	if !reflect.DeepEqual(optOut, map[string]any{"key": "pacto.trianalab.io/install", "operator": "NotIn", "values": []any{"false"}}) {
		t.Fatalf("opt-out %v", optOut)
	}
	if at(gen, "clusters", "selector", "matchLabels", "argocd.argoproj.io/secret-type") != "cluster" {
		t.Fatalf("generator %v", gen)
	}
}

func TestArgoWarnsOnMatchedCandidates(t *testing.T) {
	o := installOpts(InstallArgoCD)
	o.Matched = []ClusterCandidate{
		{Source: detect.SourceArgoCD, Namespace: "argocd", Name: "eu-prod", Match: CandidateMatched},
		{Source: detect.SourceArgoCD, Namespace: "argocd", Name: "ap-test", Match: CandidateSameUID},
		{Source: detect.SourceArgoCD, Namespace: "argocd", Name: "us-dev", Match: CandidateUnmatched},
		{Source: detect.SourceCAPI, Namespace: "fleet", Name: "eu-prod", Match: CandidateMatched},
	}
	w := mustInstall(t, o).Warnings
	if len(w) != 2 || !strings.Contains(w[0], "argocd/eu-prod") || !strings.Contains(w[0], "kubectl -n argocd label secret eu-prod pacto.trianalab.io/install=false") ||
		!strings.Contains(w[1], "argocd/ap-test") {
		t.Fatalf("warnings %q", w)
	}
	if w := mustInstall(t, installOpts(InstallArgoCD)).Warnings; w == nil || len(w) != 0 {
		t.Fatalf("no candidate gives %#v", w)
	}
}

// golden compares res's YAML with testdata/name.
func golden(t *testing.T, name string, res MemberInstallResult) {
	t.Helper()
	path := filepath.Join("testdata", name)
	got := MemberInstallYAML(res.Files)
	if *updateInstall {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch (run with -update-install to rewrite), %v:\n%s", err, got)
	}
}

func TestArgoInstallGolden(t *testing.T) {
	o := installOpts(InstallArgoCD)
	o.Argo = []detect.Candidate{{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "gitops", Name: "eu-prod"}}
	res := mustInstall(t, o)
	if res.Files[0].Path != "pacto-members-applicationset.yaml" || res.Files[1].Path != "pacto-charts-repository.yaml" {
		t.Fatalf("paths %v", res.Files)
	}
	golden(t, "memberinstall-argocd.golden.yaml", res)
}

func TestFluxInstallGolden(t *testing.T) {
	o := installOpts(InstallFlux)
	o.Flux = append(o.Flux, fluxCand("Secret", "flux-system", "eu-prod"), fluxCand("Secret", "flux-system", "ap-test"), detect.Candidate{Source: detect.SourceFlux})
	res := mustInstall(t, o)
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"clusters/pacto-chart.yaml", "clusters/pacto-us-east.yaml", "flux-system/pacto-chart.yaml",
		"flux-system/pacto-ap-test.yaml", "flux-system/pacto-eu-prod.yaml"}) {
		t.Fatalf("paths %v", paths)
	}
	golden(t, "memberinstall-flux.golden.yaml", res)
}

func TestFluxInstallNeedsACandidate(t *testing.T) {
	o := installOpts(InstallFlux)
	o.Flux = []detect.Candidate{{Source: detect.SourceFlux, Name: "no-ref"}}
	if _, err := MemberInstall(o); err == nil || !strings.Contains(err.Error(), "no Flux kubeconfig ref was detected") {
		t.Fatal(err)
	}
}

func TestFluxBootstrapPinsDigest(t *testing.T) {
	o := installOpts(InstallFlux)
	o.Digest = testDigest
	res := mustInstall(t, o)
	last := res.Files[len(res.Files)-1]
	if last.Path != "bootstrap.yaml" {
		t.Fatalf("last file %s", last.Path)
	}
	ds := docs(t, res)
	src := ds[len(ds)-2]
	if at(src, "spec", "url") != "oci://ghcr.io/acme/pacto-targets" || !reflect.DeepEqual(at(src, "spec", "ref"), map[string]any{"digest": testDigest}) ||
		at(src, "spec", "layerSelector", "mediaType") != MemberInstallLayerType {
		t.Fatalf("bootstrap source %v", src)
	}
	without := mustInstall(t, installOpts(InstallFlux))
	if !reflect.DeepEqual(without.Files, res.Files[:len(res.Files)-1]) {
		t.Fatal("the digest changed a file the push carries")
	}
	for _, f := range mustInstall(t, func() MemberInstallOptions { a := installOpts(InstallArgoCD); a.Digest = testDigest; return a }()).Files {
		if f.Path == "bootstrap.yaml" {
			t.Fatal("Argo CD got a Flux bootstrap")
		}
	}
}

// TestFluxBootstrapNeverTracksATag: nothing that reads pacto-targets, which
// every member may push to, follows a tag; the chart source pins its version.
func TestFluxBootstrapNeverTracksATag(t *testing.T) {
	o := installOpts(InstallFlux)
	o.Digest = testDigest
	for _, d := range docs(t, mustInstall(t, o)) {
		if d["kind"] != "OCIRepository" {
			continue
		}
		url, _ := at(d, "spec", "url").(string)
		ref := at(d, "spec", "ref").(map[string]any)
		switch {
		case strings.Contains(url, TargetSnapshotRepository) && (len(ref) != 1 || ref["digest"] == nil):
			t.Fatalf("the bootstrap follows %v", ref)
		case !strings.Contains(url, TargetSnapshotRepository) && !reflect.DeepEqual(ref, map[string]any{"tag": "4.6.0"}):
			t.Fatalf("the chart follows %v", ref)
		}
	}
}

func TestMemberInstallOrphansOnDeletion(t *testing.T) {
	a := docs(t, mustInstall(t, installOpts(InstallArgoCD)))[0]
	if at(a, "spec", "syncPolicy", "preserveResourcesOnDeletion") != true {
		t.Fatalf("appset %v", a)
	}
	auto := at(a, "spec", "template", "spec", "syncPolicy", "automated")
	if auto == nil || at(auto, "prune") != nil {
		t.Fatalf("automated %v", auto)
	}
	o := installOpts(InstallFlux)
	o.Digest = testDigest
	ds := docs(t, mustInstall(t, o))
	ks := ds[len(ds)-1]
	if ks["kind"] != "Kustomization" || at(ks, "spec", "prune") != false || at(ks, "spec", "deletionPolicy") != "Orphan" {
		t.Fatalf("kustomization %v", ks)
	}
}

func TestMemberInstallNamesThePushCredentialStep(t *testing.T) {
	for _, engine := range []string{InstallArgoCD, InstallFlux} {
		o := installOpts(engine)
		o.Digest = testDigest
		y := string(MemberInstallYAML(mustInstall(t, o).Files))
		if strings.Count(y, "#") != 1 || strings.Count(y, "push credential on ghcr.io/acme/pacto-targets") != 1 || !strings.HasPrefix(y, "# ") {
			t.Fatalf("%s:\n%s", engine, y)
		}
	}
}

func TestMemberInstallRefusesPlainHTTPHub(t *testing.T) {
	for _, u := range []string{"http://hub.acme.example", "ws://hub", "hub.acme.example", "https://", "https://hub/{{.server}}"} {
		o := installOpts(InstallArgoCD)
		o.HubURL = u
		if _, err := MemberInstall(o); err == nil || !strings.Contains(err.Error(), "https:// or wss://") {
			t.Fatalf("%s: %v", u, err)
		}
	}
}

func TestMemberInstallNeedsChartVersion(t *testing.T) {
	o := installOpts(InstallFlux)
	o.ChartVersion = ""
	if _, err := MemberInstall(o); err == nil || !strings.Contains(err.Error(), "--chart-version") {
		t.Fatal(err)
	}
}

// TestMemberInstallRefusesBadOptions: every option that would not install
// is refused before anything is generated.
func TestMemberInstallRefusesBadOptions(t *testing.T) {
	for name, tc := range map[string]struct {
		mod  func(*MemberInstallOptions)
		want string
	}{
		"engine":     {func(o *MemberInstallOptions) { o.Engine = "helm" }, "argocd or flux"},
		"registry":   {func(o *MemberInstallOptions) { o.Registry = "oci://ghcr.io/acme" }, "no scheme"},
		"empty":      {func(o *MemberInstallOptions) { o.Registry = "" }, "no scheme"},
		"version":    {func(o *MemberInstallOptions) { o.ChartVersion = "4.6 0" }, "not a version"},
		"chart":      {func(o *MemberInstallOptions) { o.Chart = "ghcr.io/x/pacto-operator" }, "oci://"},
		"chart path": {func(o *MemberInstallOptions) { o.Chart = "oci://ghcr.io" }, "oci://"},
		"digest":     {func(o *MemberInstallOptions) { o.Digest = "sha256:abc" }, "sha256:<64 hex>"},
		"argo ns": {func(o *MemberInstallOptions) {
			o.Engine, o.Argo = InstallArgoCD, []detect.Candidate{{Namespace: "Argo CD"}}
		}, "not a DNS label"},
		"flux kind": {func(o *MemberInstallOptions) { o.Flux = []detect.Candidate{fluxCand("Pod", "ns", "x")} }, "Secret or ConfigMap"},
		"flux ns":   {func(o *MemberInstallOptions) { o.Flux = []detect.Candidate{fluxCand("Secret", "a.b", "x")} }, "DNS name"},
		"flux name": {func(o *MemberInstallOptions) { o.Flux = []detect.Candidate{fluxCand("Secret", "ns", "X")} }, "DNS name"},
		"flux long": {func(o *MemberInstallOptions) {
			o.Flux = []detect.Candidate{fluxCand("Secret", "ns", strings.Repeat("a", 58))}
		}, "57"},
		"flux twice": {func(o *MemberInstallOptions) {
			o.Flux = []detect.Candidate{fluxCand("Secret", "ns", "x"), fluxCand("ConfigMap", "ns", "x")}
		}, "both a Secret and a ConfigMap"},
		"flux injected": {func(o *MemberInstallOptions) { o.Flux = []detect.Candidate{fluxCand("Secret", "ns", "x\n  y: z")} }, "DNS name"},
	} {
		o := installOpts(InstallFlux)
		tc.mod(&o)
		if _, err := MemberInstall(o); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestMemberInstallEscapesNothingFromInput: a label value that looks like a
// template, a quote or a line break never reaches a file, and what does
// reach one lands as a scalar.
func TestMemberInstallEscapesNothingFromInput(t *testing.T) {
	evil := "{{.server}}\"\n  injected: true"
	o := installOpts(InstallFlux)
	o.Flux[0].Labels = map[string]string{"env": evil}
	o.Flux[0].DisplayName = evil
	o.Argo = []detect.Candidate{{Namespace: "argocd", Name: "x", Labels: map[string]string{"env": evil}}}
	for _, engine := range []string{InstallArgoCD, InstallFlux} {
		o.Engine = engine
		res := mustInstall(t, o)
		y := string(MemberInstallYAML(res.Files))
		if strings.Contains(y, "injected") || strings.Contains(y, "{{.server}}\"") {
			t.Fatalf("%s:\n%s", engine, y)
		}
		for _, d := range docs(t, res) {
			if d["injected"] != nil || at(d, "spec", "injected") != nil {
				t.Fatalf("structure from input: %v", d)
			}
		}
	}
	// A scalar that YAML would read as a bool stays a string.
	if d := docs(t, mustInstall(t, installOpts(InstallArgoCD)))[1]; at(d, "stringData", "enableOCI") != "true" {
		t.Fatalf("enableOCI %#v", at(d, "stringData", "enableOCI"))
	}
}

// TestInstallArchiveHoldsTheFiles: the archive holds every file byte for byte
// and is the same bytes for the same files.
func TestInstallArchiveHoldsTheFiles(t *testing.T) {
	files := mustInstall(t, installOpts(InstallFlux)).Files
	a := InstallArchive(files)
	if !bytes.Equal(a, InstallArchive(files)) {
		t.Fatal("the archive is not reproducible")
	}
	gz, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var got []InstallFile
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got = append(got, InstallFile{Path: h.Name, Content: string(b)})
	}
	if !reflect.DeepEqual(got, files) {
		t.Fatalf("archive %v", got)
	}
}

// TestMemberInstallDownloadLeadsWithWarnings: each warning is one comment
// line before the YAML stream.
func TestMemberInstallDownloadLeadsWithWarnings(t *testing.T) {
	res := MemberInstallResult{Warnings: []string{"one\ntwo"}, Files: []InstallFile{{Path: "a.yaml", Content: "a: 1\n"}, {Path: "b.yaml", Content: "b: 2\n"}}}
	if got := string(MemberInstallDownload(res)); got != "# warning: one two\na: 1\n---\nb: 2\n" {
		t.Errorf("download = %q", got)
	}
}
