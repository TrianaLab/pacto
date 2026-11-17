package discover

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// shuffledFS lists every folder in a random order.
type shuffledFS struct{ fstest.MapFS }

func (s shuffledFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := s.MapFS.ReadDir(name)
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	return entries, err
}

func TestDiscoverDeterministic(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, dir := range []string{dockerDir, completeDir} {
		_ = fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, _ error) error {
			if !d.IsDir() {
				data, _ := os.ReadFile(filepath.Join(dir, p))
				fsys[filepath.Base(dir)+"/"+p] = &fstest.MapFile{Data: data}
			}
			return nil
		})
	}
	fsys["bad/a.yaml"], fsys["bad/b.yaml"] = &fstest.MapFile{Data: []byte("a: [")}, &fstest.MapFile{Data: []byte("b: [")}
	marshal := func() []byte {
		r := Read(Input{Folder: shuffledFS{fsys}, FolderPath: "."})
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: r.Fields, Findings: r.Findings}
		return rec.Marshal()
	}
	first := marshal()
	for range 5 {
		if got := marshal(); !bytes.Equal(got, first) {
			t.Fatalf("two reads differ:\n%s\n---\n%s", first, got)
		}
	}
}

func TestRecordNoAbsolutePaths(t *testing.T) {
	abs, _ := filepath.Abs(dockerDir)
	rec, err := Complete(abs, nil, contract.InferredInputs{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(rec.Marshal())
	if strings.Contains(out, abs) || strings.Contains(out, filepath.Dir(abs)) {
		t.Errorf("the record holds an absolute path:\n%s", out)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Error(err)
	}
}

func TestDiscoveryTruncatedNamed(t *testing.T) {
	defer func(n int) { maxFiles = n }(maxFiles)
	maxFiles = 150
	fsys := fstest.MapFS{}
	for i := range 110 {
		fsys[fmt.Sprintf("a%03d", i)] = &fstest.MapFile{Mode: fs.ModeNamedPipe}
	}
	for i := range 50 {
		fsys[fmt.Sprintf("z%03d", i)] = &fstest.MapFile{}
	}
	r := Read(Input{Folder: fsys, FolderPath: "."})
	if len(r.Findings) != MaxFindings+1 || r.FindingsDropped != 10 {
		t.Fatalf("%d findings, %d dropped", len(r.Findings), r.FindingsDropped)
	}
	if f := r.Findings[0]; f.Code != CodeTruncated || f.Message != "discovery stopped after 150 files; 10 files were not read, so results are partial" {
		t.Errorf("first finding = %+v", f)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Findings: slices.Concat(r.Findings[1:], r.Findings[:1]), Inputs: contract.InferredInputs{Sources: []contract.InferredSourceFile{}}}
	parsed, err := contract.ParseInferredRecord(rec.Marshal())
	if err != nil || parsed.Findings[0].Code != CodeTruncated {
		t.Errorf("the record does not order truncation first: %v", err)
	}
}

func TestFindingMessagesAreFixed(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"values.yaml":            "password: hunter2\n  nested: [hunter2\n",
		"Dockerfile":             "FROM x\nEXPOSE ${hunter2}\n",
		"chart/Chart.yaml":       "name: c\n",
		"chart/templates/a.yaml": "{{ .Values.hunter2 }}: [\n",
		"other/templates/b.yaml": "b: [\n",
	})
	r := readFixture(t, dir)
	want := []contract.InferredFinding{
		{Code: CodeUnreadable, Source: "other/templates/b.yaml", Message: "other/templates/b.yaml is not valid YAML, so it is not read"},
		{Code: CodeUnreadable, Source: "values.yaml", Message: "values.yaml is not valid YAML, so it is not read"},
		{Code: CodeUnresolved, Source: "Dockerfile:2", Message: "EXPOSE at Dockerfile:2 names a port through a variable or a range, so it is not read"},
	}
	if !slices.Equal(r.Findings, want) {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestDiscoveryUnreadableFiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nEXPOSE 80\n" + strings.Repeat("#", maxFileSize), "locked.yaml": "a: b\n"})
	if err := os.Chmod(filepath.Join(dir, "locked.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	r := readFixture(t, dir)
	want := []contract.InferredFinding{
		{Code: CodeUnreadable, Source: "Dockerfile", Message: "Dockerfile is over 1 MiB, so it is not read"},
		{Code: CodeUnreadable, Source: "locked.yaml", Message: "locked.yaml could not be read"},
	}
	if !slices.Equal(r.Findings, want) || len(r.Fields) != 0 {
		t.Errorf("findings = %+v, fields = %+v", r.Findings, r.Fields)
	}
}

// failingFS fails to list the folder sub.
type failingFS struct{ fstest.MapFS }

func (f failingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "sub" {
		return nil, errors.New("boom")
	}
	return f.MapFS.ReadDir(name)
}

func TestDiscoveryUnlistableFolder(t *testing.T) {
	r := Read(Input{Folder: failingFS{fstest.MapFS{"sub/x": {}, ".git/config": {}, "node_modules/y": {}}}})
	if len(r.Findings) != 1 || r.Findings[0].Source != "sub" || r.Findings[0].Message != "sub could not be read" {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestRepoRootStopsAtGit(t *testing.T) {
	root := writeFiles(t, map[string]string{"a/b/c.txt": "", "wt/.git": "gitdir: /elsewhere\n", "wt/x/y.txt": ""})
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{"a/b": root, "wt/x": filepath.Join(root, "wt"), ".": root} {
		if got, ok := RepoRoot(filepath.Join(root, dir)); !ok || got != want {
			t.Errorf("RepoRoot(%s) = %q, %v; want %q", dir, got, ok, want)
		}
	}
	if got, ok := RepoRoot(t.TempDir()); ok {
		t.Errorf("a folder outside any repository has the root %q", got)
	}
}

func TestReadDirRoots(t *testing.T) {
	if r, err := ReadDir(filepath.Join(t.TempDir(), "missing"), "", ""); err != nil || len(r.Fields)+len(r.Findings) != 0 {
		t.Errorf("a missing folder: %+v, %v", r, err)
	}
	file := filepath.Join(writeFiles(t, map[string]string{"f": ""}), "f")
	if _, err := ReadDir(file, "", ""); err == nil {
		t.Error("a file read as a folder")
	}
	dir := writeFiles(t, map[string]string{".pactoignore/x": ""})
	if _, err := ReadDir(dir, "", ""); err == nil || !strings.Contains(err.Error(), ".pactoignore") {
		t.Errorf("an unreadable .pactoignore: %v", err)
	}
	dir = writeFiles(t, map[string]string{"svc/Dockerfile": "FROM x\nEXPOSE 80\n", "svc/.pactoignore": "Dockerfile\n"})
	if r, err := ReadDir(filepath.Join(dir, "svc"), dir, ""); err != nil || len(r.Fields) != 0 {
		t.Errorf("an ignored Dockerfile was read: %+v, %v", r, err)
	}
	if _, err := ReadDir(filepath.Join(dir, "svc"), filepath.Join(dir, "missing"), ""); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("a folder outside the repository root read: %v", err)
	}
	if _, err := CompleteIn(filepath.Join(dir, "svc"), filepath.Join(dir, "missing"), nil, contract.InferredInputs{}); err == nil {
		t.Error("CompleteIn read a missing repository root")
	}
}

func TestCiteRepositorySources(t *testing.T) {
	root := writeFiles(t, map[string]string{".github/CODEOWNERS": "* @acme/team\n", "svc/a": "x"})
	got, err := citeSources(filepath.Join(root, "svc"), root, map[string]bool{"repository:.github/CODEOWNERS": true, "a": true})
	if err != nil || len(got) != 2 || got[1].Path != "repository:.github/CODEOWNERS" || got[0].Path != "a" {
		t.Errorf("sources = %+v, %v", got, err)
	}
	if _, err := citeSources(root, root, map[string]bool{"repository:missing": true}); err == nil {
		t.Error("a missing repository file was cited")
	}
}

func TestCompleteRecordsDockerfilePort(t *testing.T) {
	before, err := os.ReadFile(filepath.Join(dockerDir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := CompleteIn(dockerDir, dockerDir, &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "orders"}}, contract.InferredInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if f := field(t, rec.Fields, "ports[8080]"); f.Source != "Dockerfile:12" || f.Confidence != contract.ConfidenceLikely {
		t.Errorf("ports[8080] = %+v", f)
	}
	after, _ := os.ReadFile(filepath.Join(dockerDir, "pacto.yaml"))
	if !bytes.Equal(before, after) {
		t.Error("pacto.yaml changed")
	}
}

func TestCompleteFindingsOnly(t *testing.T) {
	dir := writeFiles(t, map[string]string{"broken.yaml": "a: ["})
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{})
	if err != nil || rec == nil || len(rec.Fields) != 0 || len(rec.Findings) != 1 {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

func TestCompleteVersionBumped(t *testing.T) {
	rec, err := Complete(dockerDir, nil, contract.InferredInputs{})
	if err != nil || rec.Inputs.Discovery != "2" {
		t.Fatalf("discovery = %+v, %v", rec, err)
	}
}

func TestDiscoveryRecordParses(t *testing.T) {
	for _, dir := range []string{dockerDir, completeDir} {
		r := readFixture(t, dir)
		rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{Pacto: "4.6.0"})
		if err != nil {
			t.Fatal(err)
		}
		rec.Findings = append(rec.Findings, r.Findings...)
		if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

// One file that sets a field twice keeps its earlier line, by number: line 7
// outranks line 10, and outranks list lines in number order.
func TestMergeOrdersLinesByNumber(t *testing.T) {
	e := func(src string) entry {
		return entry{Inferred: contract.Inferred{Field: "ports[80]", Source: src}, rank: rankChart, file: "values.yaml"}
	}
	fields, _ := merge([]entry{e("values.yaml:10"), e("values.yaml:12"), e("values.yaml:7"), e("values.yaml:9")})
	if got := fields[0]; got.Source != "values.yaml:7" || !slices.Equal(got.Outranks, []string{"values.yaml:9", "values.yaml:10", "values.yaml:12"}) {
		t.Errorf("merged = %+v", got)
	}
}

// A file or folder whose name has a ':' is one finding on the folder that
// holds it, so the record discovery writes is one its decoder reads.
func TestColonNameNeverReachesASource(t *testing.T) {
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nEXPOSE 8080\n", "bad:file.yaml": "a: [", "sub/x:y.yaml": "a: [",
		"cfg:v1/Dockerfile": "FROM x\nEXPOSE 9090\n"})
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{})
	if err != nil || rec == nil {
		t.Fatalf("record = %v, %v", rec, err)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatalf("the record is refused: %v\n%s", err, rec.Marshal())
	}
	var sources []string
	for _, f := range rec.Findings {
		sources = append(sources, f.Source)
	}
	if !slices.Equal(sources, []string{".", "sub"}) {
		t.Errorf("finding sources = %v", sources)
	}
}
