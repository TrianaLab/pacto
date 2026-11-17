package discover

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
)

// The bounds of one read: at most MaxFiles files walked and MaxFindings
// findings recorded, the truncation finding aside.
const (
	MaxFiles    = 5000
	MaxFindings = 100
)

// maxFiles is MaxFiles, lowered by tests.
var maxFiles = MaxFiles

// maxFileSize is the largest file discovery reads, as contract.ReadBounded.
const maxFileSize = 1 << 20

// Input is what Read reads: the service folder (with .pactoignore applied),
// the repository root that holds it (nil means the folder is the repository),
// the folder's slash path in the repository ("." when it is the repository)
// and the service name.
type Input struct {
	Folder     fs.FS
	Repo       fs.FS
	FolderPath string
	Name       string
}

// Reading is what Read found: the fields, each with its source, ranked once
// per field; the findings, at most MaxFindings with the truncation finding
// first and FindingsDropped counting the rest; and the chart dependencies
// Chart.lock pins.
type Reading struct {
	Fields            []contract.Inferred
	Findings          []contract.InferredFinding
	FindingsDropped   int
	ChartDependencies []contract.InferredChartDependency
	entries           []entry // Fields before the merge, for Complete's
}

// entry is one field a reader gives, its rank (lower outranks higher) and the
// file it cites: a folder path, or "repository:<path>".
type entry struct {
	contract.Inferred
	rank int
	file string
}

// Ranks: the folder scan Complete runs (the workload probe, the team label)
// first, then each Dockerfile in dockerRank order, then a chart's values.yaml
// and its values-<env>.yaml files, then its templates, then a kustomization's
// images, then a Compose service's image and environment keys, then plain
// manifests, then a Compose service's ports. An owner ranks apart: the
// catalog file, then CODEOWNERS, then the workload's team label.
const (
	rankScan        = 0
	rankDockerfile  = 1 << 20
	rankChart       = 2 << 20
	rankTemplate    = 3 << 20
	rankKustomize   = 4 << 20
	rankCompose     = 5 << 20
	rankManifest    = 6 << 20
	rankComposePort = 7 << 20
	rankCatalog     = 8 << 20
	rankCodeowners  = 9 << 20
	rankTeamLabel   = 10 << 20
)

// reader collects what one Read finds.
type reader struct {
	in          Input
	entries     []entry
	findings    []contract.InferredFinding
	dockerfiles []string
	specs       map[string][]spec     // by kind
	chartFiles  map[string]*yaml.Node // the root of each Chart.yaml, Chart.lock and values file, by path
	chartDeps   []contract.InferredChartDependency
	templates   []string // the chart templates, read once the charts are known
}

// Read walks the folder once, regular files only, and hands each file to the
// reader of its kind. A link, a FIFO or a device is never opened: it is a
// finding, as is a file over 1 MiB or one that does not parse. Read never
// fails; what it cannot read is a finding.
func Read(in Input) Reading {
	r := &reader{in: in, specs: map[string][]spec{}, chartFiles: map[string]*yaml.Node{}}
	walked, left := 0, 0
	_ = fs.WalkDir(in.Folder, ".", func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback never fails
		switch {
		case err != nil:
			r.find(CodeUnreadable, p, unreadableMessage(p, unreadableFailed))
			return nil
		case d.IsDir():
			if p != "." && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return fs.SkipDir
			}
			return nil
		case walked >= maxFiles:
			left++
			return nil
		}
		walked++
		if !d.Type().IsRegular() {
			r.find(CodeUnreadable, p, unreadableMessage(p, unreadableNotRegular))
			return nil
		}
		r.file(p, d.Name())
		return nil
	})
	r.dockers()
	r.interfaces()
	r.charts()
	r.codeowners()
	out := Reading{entries: r.entries, ChartDependencies: r.chartDeps}
	out.Fields, _ = merge(r.entries)
	slices.SortFunc(r.findings, func(a, b contract.InferredFinding) int {
		return cmp.Or(strings.Compare(a.Code, b.Code), strings.Compare(a.Source, b.Source))
	})
	if len(r.findings) > MaxFindings {
		out.FindingsDropped = len(r.findings) - MaxFindings
		r.findings = r.findings[:MaxFindings]
	}
	if left > 0 {
		r.findings = slices.Insert(r.findings, 0, contract.InferredFinding{Code: CodeTruncated, Source: ".", Message: truncatedMessage(walked, left)})
	}
	out.Findings = r.findings
	return out
}

// file hands the regular file p to the reader of its kind.
func (r *reader) file(p, name string) {
	switch {
	case name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") || strings.HasSuffix(name, ".Dockerfile"):
		r.dockerfiles = append(r.dockerfiles, p)
	case name == "Chart.lock":
		if data := r.read(p); data != nil {
			r.keepChartFile(p, name, r.yamlDocs(p, data))
		}
	case strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"):
		r.yamlFile(p, name)
	case strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".proto"):
		if data := r.read(p); data != nil {
			r.spec(p, data)
		}
	default:
		r.unsupported(p, name)
	}
}

// yamlFile reads the YAML file p, named name: a chart's template is kept for
// the template scan, since it is not YAML until rendered; any other file is
// parsed for the Helm reader and the spec reader, and, outside a chart, for
// the workloads it holds.
func (r *reader) yamlFile(p, name string) {
	chart, inChart := r.chartOf(p)
	if inChart && strings.HasPrefix(p, path.Join(chart, "templates")+"/") {
		r.templates = append(r.templates, p)
		return
	}
	data := r.read(p)
	if data == nil {
		return
	}
	if docs := r.yamlDocs(p, data); docs != nil {
		r.keepChartFile(p, name, docs)
		r.spec(p, data)
		if !inChart {
			r.manifests(p, docs)
			r.tool(p, name, docs)
		}
	}
}

// tool reads the first document of the file p, named name, when it is a
// kustomization or, at the folder root, a Compose file or the Backstage
// catalog file. yamlDocs gives docs
// at least one document, each with a root, as manifests relies on.
func (r *reader) tool(p, name string, docs []*yaml.Node) {
	switch root := docs[0].Content[0]; {
	case name == "kustomization.yaml" || name == "kustomization.yml":
		r.kustomization(p, root)
	case composeFiles[p]:
		r.compose(p, root)
	case p == catalogFile:
		r.catalog(p, docs)
	}
}

// chartOf is the closest folder at or above p's that holds a Chart.yaml,
// ok false when there is none.
func (r *reader) chartOf(p string) (dir string, ok bool) {
	for dir = path.Dir(p); ; dir = path.Dir(dir) {
		if _, err := fs.Stat(r.in.Folder, path.Join(dir, "Chart.yaml")); err == nil {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
	}
}

// read is the bytes of the regular file p, or nil and a finding.
func (r *reader) read(p string) []byte { return r.readIn(r.in.Folder, p, p) }

// readIn is the bytes of the regular file p in fsys, or nil and a finding
// citing it as source.
func (r *reader) readIn(fsys fs.FS, p, source string) []byte {
	if info, err := fs.Stat(fsys, p); err == nil && info.Size() > maxFileSize {
		r.find(CodeUnreadable, source, unreadableMessage(source, unreadableTooLarge))
		return nil
	}
	data, ok := contract.ReadBounded(fsys, p)
	if !ok {
		r.find(CodeUnreadable, source, unreadableMessage(source, unreadableFailed))
		return nil
	}
	return data
}

// yamlDocs is every document of data, the YAML file p, or nil and a finding
// when it does not parse. The parser's error is never kept: it quotes the
// file.
func (r *reader) yamlDocs(p string, data []byte) []*yaml.Node {
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				r.find(CodeUnreadable, p, unreadableMessage(p, unreadableNotYAML))
				return nil
			}
			return docs
		}
		docs = append(docs, &doc)
	}
}

func (r *reader) find(code, source, message string) {
	r.findings = append(r.findings, contract.InferredFinding{Code: code, Source: source, Message: message})
}

func (r *reader) add(e entry) { r.entries = append(r.entries, e) }

// merge keeps one field per field name: the entry of lowest rank (then
// source) writes it and every other source goes, sorted, into its outranks.
// cited is every file a kept field or an outranks entry names.
func merge(entries []entry) (fields []contract.Inferred, cited map[string]bool) {
	byField := map[string][]entry{}
	for _, e := range entries {
		byField[e.Field] = append(byField[e.Field], e)
	}
	cited = map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(byField)) {
		group := byField[name]
		slices.SortFunc(group, func(a, b entry) int { return cmp.Or(cmp.Compare(a.rank, b.rank), strings.Compare(a.Source, b.Source)) })
		f := group[0].Inferred
		f.Outranks = nil
		for _, o := range group[1:] {
			if o.Source != f.Source && !slices.Contains(f.Outranks, o.Source) {
				f.Outranks = append(f.Outranks, o.Source)
			}
			cited[o.file] = true
		}
		slices.Sort(f.Outranks)
		cited[group[0].file] = true
		fields = append(fields, f)
	}
	return fields, cited
}

// ReadDir reads the folder dir, inside the repository root repoDir: "" finds
// the root with RepoRoot, and with none the folder is its own repository. A
// folder that does not exist yet reads as empty. Both roots are opened with
// os.OpenRoot, so no read leaves them.
func ReadDir(dir, repoDir string) (Reading, error) {
	abs, _ := filepath.Abs(dir) // ponytail: Abs fails only without a working directory, and OpenRoot("") then fails
	root, err := os.OpenRoot(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return Reading{}, nil
	}
	if err != nil {
		return Reading{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	ign, err := ignore.Load(root.FS())
	if err != nil {
		return Reading{}, fmt.Errorf("reading %s: %w", filepath.Join(dir, ignore.IgnoreFileName), err)
	}
	in := Input{Folder: ignore.FS(root.FS(), ign), FolderPath: ".", Name: filepath.Base(abs)}
	if repo := repoFor(abs, repoDir); repo != abs {
		rr, err := os.OpenRoot(repo)
		if err != nil {
			return Reading{}, fmt.Errorf("reading the repository %s: %w", repo, err)
		}
		defer func() { _ = rr.Close() }()
		rel, _ := filepath.Rel(repo, abs) // ponytail: repo is abs or one of its parents
		in.Repo, in.FolderPath = rr.FS(), filepath.ToSlash(rel)
	}
	return Read(in), nil
}

// repoFor is the repository root of the absolute folder abs: repoDir when
// set, else the one RepoRoot finds, else abs.
func repoFor(abs, repoDir string) string {
	if repoDir != "" {
		r, _ := filepath.Abs(repoDir) // ponytail: Abs fails only without a working directory, which abs already needed
		return r
	}
	if r, ok := RepoRoot(abs); ok {
		return r
	}
	return abs
}

// RepoRoot is the closest folder at or above dir that holds a .git entry (a
// directory, or the file a linked worktree or a submodule has).
func RepoRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
