package contract

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

const (
	chartYAML   = "apiVersion: v2\nname: from-chart\nversion: 0.1.0\n"
	kustomYAML  = "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nmetadata:\n  name: from-kustomize\n"
	composeYAML = "services:\n  from-compose:\n    image: x\n"
	dockerfile  = "FROM scratch\nLABEL org.opencontainers.image.title=from-label\n"
	deployYAML  = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\n  labels:\n    app.kubernetes.io/name: from-k8s\n"
)

func TestInferNamePrecedence(t *testing.T) {
	all := fstest.MapFS{
		"chart/Chart.yaml":   file(chartYAML),
		"kustomization.yaml": file(kustomYAML),
		"compose.yaml":       file(composeYAML),
		"Dockerfile":         file(dockerfile),
		"deploy/app.yaml":    file(deployYAML),
	}
	drop := func(keys ...string) fstest.MapFS {
		m := fstest.MapFS{}
		for k, v := range all {
			m[k] = v
		}
		for _, k := range keys {
			delete(m, k)
		}
		return m
	}
	tests := []struct {
		name       string
		fsys       fs.FS
		want, from string
	}{
		{"chart beats kustomize and compose", all, "from-chart", "chart/Chart.yaml:2"},
		{"kustomize beats compose", drop("chart/Chart.yaml"), "from-kustomize", "kustomization.yaml:4"},
		{"compose beats the image label", drop("chart/Chart.yaml", "kustomization.yaml"), "from-compose", "compose.yaml:2"},
		{"image label beats app.kubernetes.io/name", drop("chart/Chart.yaml", "kustomization.yaml", "compose.yaml"), "from-label", "Dockerfile:2"},
		{"app.kubernetes.io/name beats the directory", drop("chart/Chart.yaml", "kustomization.yaml", "compose.yaml", "Dockerfile"), "from-k8s", "deploy/app.yaml:6"},
		{"directory last", fstest.MapFS{}, "svc", "directory svc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := InferName(tt.fsys, "svc")
			want := Inferred{Field: "service.name", Value: tt.want, Source: tt.from}
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("InferName = %+v, %v; want %+v", got, ok, want)
			}
		})
	}
}

func TestInferNameChartLine(t *testing.T) {
	got, _ := InferName(fstest.MapFS{"chart/Chart.yaml": file("apiVersion: v2\nname: payments\n")}, "x")
	if got.Value != "payments" || got.Source != "chart/Chart.yaml:2" {
		t.Fatalf("got %+v", got)
	}
}

func TestInferNameAmbiguousChartFallsThrough(t *testing.T) {
	got, _ := InferName(fstest.MapFS{
		"a/Chart.yaml": file("name: one\n"),
		"b/Chart.yaml": file("name: two\n"),
		"Dockerfile":   file(dockerfile),
	}, "x")
	if got.Value != "from-label" {
		t.Fatalf("got %+v", got)
	}
	// Two charts that agree count once; the shallowest one wins over deeper ones.
	got, _ = InferName(fstest.MapFS{
		"a/Chart.yaml":     file("name: one\n"),
		"b/Chart.yaml":     file("name: one\n"),
		"c/sub/Chart.yaml": file("name: deeper\n"),
	}, "x")
	if got.Value != "one" || got.Source != "a/Chart.yaml:1" {
		t.Fatalf("got %+v", got)
	}
}

func TestInferNameComposeNeedsOneService(t *testing.T) {
	got, _ := InferName(fstest.MapFS{"docker-compose.yml": file("services:\n  a: {}\n  b: {}\n")}, "dir")
	if got.Value != "dir" {
		t.Fatalf("two services must fall through, got %+v", got)
	}
}

func TestInferNameDockerfileLabelForms(t *testing.T) {
	tests := map[string]string{
		"several pairs":  "FROM x\nLABEL a=b org.opencontainers.image.title=\"quoted\"\n",
		"single quotes":  "FROM x\nlabel org.opencontainers.image.title='quoted'\n",
		"continuation":   "FROM x\nLABEL a=\"b c\" \\\n  org.opencontainers.image.title=quoted\n",
		"quoted key":     "# comment\n\nFROM x\nLABEL \"org.opencontainers.image.title\"=quoted\n",
		"trailing slash": "FROM x\nLABEL org.opencontainers.image.title=quoted \\",
	}
	for name, df := range tests {
		t.Run(name, func(t *testing.T) {
			got, _ := InferName(fstest.MapFS{"Dockerfile": file(df)}, "dir")
			if got.Value != "quoted" || !strings.HasPrefix(got.Source, "Dockerfile:") {
				t.Fatalf("got %+v", got)
			}
		})
	}
	got, _ := InferName(fstest.MapFS{"Dockerfile": file("FROM x\nLABEL\nRUN true\n")}, "dir")
	if got.Value != "dir" {
		t.Fatalf("got %+v", got)
	}
}

func TestInferNameInvalidCandidateFallsThrough(t *testing.T) {
	got, _ := InferName(fstest.MapFS{
		"Chart.yaml": file("name: Pay_Ments\n"),
		"Dockerfile": file(dockerfile),
	}, "x")
	if got.Value != "from-label" {
		t.Fatalf("got %+v", got)
	}
}

func TestInferNameDirectorySanitized(t *testing.T) {
	got, ok := InferName(nil, "My Service!")
	if !ok || got.Value != "my-service" || got.Source != "directory My Service!" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := InferName(nil, "!!!"); ok {
		t.Fatal("a directory with no usable character names nothing")
	}
}

func TestInferNameIgnoredFileNotRead(t *testing.T) {
	// The bundle FS is what .pactoignore leaves: a chart outside it is no source.
	got, _ := InferName(fstest.MapFS{"pacto.yaml": file("")}, "svc")
	if got.Value != "svc" {
		t.Fatalf("got %+v", got)
	}
}

func TestInferNameSkipsChartTemplatesAndVendored(t *testing.T) {
	got, _ := InferName(fstest.MapFS{
		"Chart.yaml":               file("name: Invalid_\n"),
		"templates/deploy.yaml":    file(deployYAML),
		"node_modules/x/app.yaml":  file(deployYAML),
		".hidden/app.yaml":         file(deployYAML),
		"a/b/c/d/app.yaml":         file(deployYAML),
		"big.yaml":                 {Data: make([]byte, maxInferFileSize+1)},
		"multi.yaml":               file("---\n- a\n---\nkind: X\n"),
		"broken.yaml":              file(":\n  - ]["),
		"kustomization.yml":        file("resources: []\n"),
		"other/kustomization.yaml": file("metadata:\n  name: Bad_Name\n"),
		"compose.yml":              file("services: []\n"),
	}, "dir")
	if got.Value != "dir" {
		t.Fatalf("got %+v", got)
	}
	got, _ = InferName(fstest.MapFS{
		"chart/Chart.yaml":           file("name: Bad_\n"),
		"chart/templates/x.yaml":     file(deployYAML),
		"big.yaml":                   {Data: make([]byte, maxInferFileSize+1)},
		"Dockerfile":                 {Data: make([]byte, maxInferFileSize+1)},
		"deploy/overlays/prod/x.yml": file(deployYAML),
	}, "dir")
	if got.Value != "from-k8s" || got.Source != "deploy/overlays/prod/x.yml:6" {
		t.Fatalf("got %+v", got)
	}
}

func TestSanitizeName(t *testing.T) {
	if got := SanitizeName("--Hello__World--"); got != "hello-world" {
		t.Fatalf("got %q", got)
	}
}

func TestNameCollisions(t *testing.T) {
	a := NameClaim{Dir: "a", Name: "pay", Source: "directory a", Inferred: true}
	b := NameClaim{Dir: "b", Name: "pay", Source: "b/Chart.yaml:2", Inferred: true}
	d := NameClaim{Dir: "d", Name: "pay"}
	e := NameClaim{Dir: "e", Name: "pay"}
	lone := NameClaim{Dir: "z", Name: "solo", Inferred: true}
	tests := []struct {
		name   string
		claims []NameClaim
		want   []Collision
	}{
		{"two inferred", []NameClaim{b, a, lone}, []Collision{{Name: "pay", Claims: []NameClaim{a, b}}}},
		{"inferred and declared", []NameClaim{d, a}, []Collision{{Name: "pay", Claims: []NameClaim{a, d}}}},
		{"two declared", []NameClaim{d, e}, nil},
		{"sorted by name", []NameClaim{a, d, {Dir: "y", Name: "alpha", Inferred: true}, {Dir: "x", Name: "alpha"}},
			[]Collision{{Name: "alpha", Claims: []NameClaim{{Dir: "x", Name: "alpha"}, {Dir: "y", Name: "alpha", Inferred: true}}}, {Name: "pay", Claims: []NameClaim{a, d}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NameCollisions(tt.claims); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestReadBoundedRefusesNonRegular(t *testing.T) {
	fsys := fstest.MapFS{
		"ok":   file("data"),
		"link": {Data: []byte("target"), Mode: fs.ModeSymlink},
		"fifo": {Mode: fs.ModeNamedPipe},
		"big":  {Data: make([]byte, maxInferFileSize+1)},
		"dir":  {Mode: fs.ModeDir},
	}
	if data, ok := ReadBounded(fsys, "ok"); !ok || string(data) != "data" {
		t.Errorf("ok = %q, %v", data, ok)
	}
	for _, p := range []string{"link", "fifo", "big", "dir", "missing"} {
		if _, ok := ReadBounded(fsys, p); ok {
			t.Errorf("%s was read", p)
		}
	}
	// A file that grows past the bound between Stat and Read is refused.
	if _, ok := ReadBounded(growingFS{}, "x"); ok {
		t.Error("a file over the bound after its Stat was read")
	}
}

// growingFS serves one file whose Stat says it is empty and whose reads
// never end.
type growingFS struct{}

func (growingFS) Open(string) (fs.File, error) { return growingFile{}, nil }

type growingFile struct{}

func (growingFile) Stat() (fs.FileInfo, error) { return fstest.MapFS{"x": file("")}.Stat("x") }
func (growingFile) Read(p []byte) (int, error) { return len(p), nil }
func (growingFile) Close() error               { return nil }
