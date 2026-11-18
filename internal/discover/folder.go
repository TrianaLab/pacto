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
	"strconv"
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
// and the service name. Values, when set, is called with each
// non-sensitive string value the readers parse (Helm values files, Compose
// environment, Kustomize configMapGenerator); a value is never kept in
// Reading. A nil Values changes nothing.
type Input struct {
	Folder     fs.FS
	Repo       fs.FS
	FolderPath string
	Name       string
	Values     func(Value)
}

// Value is one string value a reader parsed, the file and line it sits at,
// the kind of file and the key it sits under.
type Value struct {
	Kind, Path string
	Line       int
	Key, Value string
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
	// ambiguousSpecs are the files of each DISCOVERY_AMBIGUOUS spec
	// finding, by its source, so Complete drops the finding when the
	// contract declares one of them.
	ambiguousSpecs map[string][]string
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
// catalog file, then CODEOWNERS, then the workload's team label. So does an
// environment's file: environments/<env>.yaml, then values-<env>.yaml, then
// an overlays/<env>/ kustomization, then a GitOps object that names it.
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
	rankEnvFile     = 11 << 20
	rankEnvValues   = 12 << 20
	rankEnvOverlay  = 13 << 20
	rankGitOps      = 14 << 20
)

// reader collects what one Read finds.
type reader struct {
	in          Input
	entries     []entry
	findings    []contract.InferredFinding
	dockerfiles []string
	specs       map[string][]spec     // by kind
	ambiguous   map[string][]string   // the files of each ambiguous spec finding, by its source
	chartFiles  map[string]*yaml.Node // the root of each Chart.yaml, Chart.lock and values file, by path
	chartDeps   []contract.InferredChartDependency
	templates   []string        // the chart templates, read once the charts are known
	walked      map[string]bool // every regular file the walk visited
	envs        []envsFile      // the configMapGenerator envs files, read after the walk
}

// Read walks the folder once, regular files only, and hands each file to the
// reader of its kind. A link, a FIFO or a device is never opened: it is a
// finding, as is a file over 1 MiB or one that does not parse. Read never
// fails; what it cannot read is a finding.
func Read(in Input) Reading {
	r := &reader{in: in, specs: map[string][]spec{}, chartFiles: map[string]*yaml.Node{}, ambiguous: map[string][]string{}, walked: map[string]bool{}}
	walked, left := 0, 0
	_ = fs.WalkDir(in.Folder, ".", func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback never fails
		switch {
		case err != nil:
			r.find(CodeUnreadable, p, unreadableMessage(p, unreadableFailed))
			return nil
		case strings.Contains(d.Name(), ":"):
			// A source is "<path>[:<line>]", so the record refuses a path with
			// a ':': the folder holding it is the finding's source.
			r.find(CodeUnreadable, path.Dir(p), unreadableMessage(path.Dir(p), unreadableColon))
			if d.IsDir() {
				return fs.SkipDir
			}
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
		r.walked[p] = true
		r.file(p, d.Name())
		return nil
	})
	r.generatorEnvs()
	r.dockers()
	r.interfaces()
	r.charts()
	r.codeowners()
	out := Reading{entries: r.entries, ChartDependencies: r.chartDeps, ambiguousSpecs: r.ambiguous}
	out.Fields, _ = merge(r.entries)
	slices.SortFunc(r.findings, func(a, b contract.InferredFinding) int {
		return cmp.Or(strings.Compare(a.Code, b.Code), strings.Compare(a.Source, b.Source), strings.Compare(a.Message, b.Message))
	})
	// One line can raise the same finding twice (EXPOSE $A $B, a flow list
	// of remote resources): it is named once.
	r.findings = slices.Compact(r.findings)
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

// toolJSON are JSON files package tools write, never an interface spec, so
// a lockfile over 1 MiB is no finding.
// ponytail: the common names; the next one is one row.
var toolJSON = map[string]bool{"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "composer.json": true, "tsconfig.json": true}

// specFile reports whether the file name may hold a JSON or proto spec.
func specFile(name string) bool {
	return strings.HasSuffix(name, ".proto") || (strings.HasSuffix(name, ".json") && !toolJSON[name])
}

// file hands the regular file p to the reader of its kind.
func (r *reader) file(p, name string) {
	switch {
	case name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") || strings.HasSuffix(name, ".Dockerfile"):
		r.dockerfiles = append(r.dockerfiles, p)
	case r.unsupported(p, name):
	case path.Dir(p) == envDir && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")):
		r.envFile(p, name)
	case name == "Chart.lock":
		if data := r.read(p); data != nil {
			r.keepChartFile(p, name, r.yamlDocs(p, data))
		}
	case strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"):
		r.yamlFile(p, name)
	case specFile(name):
		if data := r.read(p); data != nil {
			r.spec(p, data)
		}
	}
}

// yamlFile reads the YAML file p, named name: a chart's template is kept for
// the template scan, since it is not YAML until rendered; any other file is
// parsed for the Helm reader and the spec reader, and, unless it is the
// chart's own (its Chart.yaml, a values file, a subchart or a CRD), for the
// workloads, GitOps objects and tool files it holds. So a folder that is a
// chart still has its catalog file, Compose file and overlays read.
func (r *reader) yamlFile(p, name string) {
	chart, inChart := r.chartOf(p)
	under := func(sub string) bool { return strings.HasPrefix(p, path.Join(chart, sub)+"/") }
	if inChart && under("templates") {
		r.templates = append(r.templates, p)
		return
	}
	inChart = inChart && (under("charts") || under("crds") ||
		(path.Dir(p) == chart && (name == "Chart.yaml" || valuesRE.MatchString(name))))
	data := r.read(p)
	if data == nil {
		return
	}
	docs := r.yamlDocs(p, data)
	r.keepChartFile(p, name, docs)
	if docs != nil {
		r.spec(p, data)
		if !inChart {
			r.manifests(p, docs)
			r.gitops(p, docs)
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
// file, then line) writes it and every other source goes, sorted, into its outranks.
// cited is every file a kept field or an outranks entry names.
func merge(entries []entry) (fields []contract.Inferred, cited map[string]bool) {
	byField := map[string][]entry{}
	for _, e := range entries {
		byField[e.Field] = append(byField[e.Field], e)
	}
	cited = map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(byField)) {
		group := byField[name]
		slices.SortFunc(group, func(a, b entry) int { return cmp.Or(cmp.Compare(a.rank, b.rank), sourceOrder(a.Source, b.Source)) })
		f := group[0].Inferred
		f.Outranks = nil
		for _, o := range group[1:] {
			if o.Source != f.Source && !slices.Contains(f.Outranks, o.Source) {
				f.Outranks = append(f.Outranks, o.Source)
			}
			cited[o.file] = true
		}
		slices.SortFunc(f.Outranks, sourceOrder)
		cited[group[0].file] = true
		fields = append(fields, f)
	}
	return fields, cited
}

// sourceOrder orders "<path>[:<line>]" sources by path, then by line as a
// number, so line 7 of a file comes before its line 10.
func sourceOrder(a, b string) int {
	pa, la := splitLine(a)
	pb, lb := splitLine(b)
	return cmp.Or(strings.Compare(pa, pb), cmp.Compare(la, lb))
}

// splitLine is the path and the line of a "<path>[:<line>]" source, line 0
// when it names none.
func splitLine(s string) (string, int) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i], n
		}
	}
	return s, 0
}

// readDir reads the folder dir, inside the repository root repoDir: "" finds
// the root with RepoRoot, and with none the folder is its own repository.
// name is the service name the readers pick Compose services, catalog
// Components and Kustomize images by; "" is the folder's base name. values
// is the Input.Values visitor. A folder that does not exist yet reads as
// empty. Both roots are opened with os.OpenRoot, so no read leaves them.
func readDir(dir, repoDir, name string, values func(Value)) (Reading, error) {
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
	in := Input{Folder: ignore.FS(root.FS(), ign), FolderPath: ".", Name: cmp.Or(name, filepath.Base(abs)), Values: values}
	if repo := repoFor(abs, repoDir); repo != abs {
		rel, _ := filepath.Rel(repo, abs) // ponytail: both are absolute
		if !filepath.IsLocal(rel) {
			return Reading{}, fmt.Errorf("reading %s: it is outside the repository %s", dir, repo)
		}
		rr, err := os.OpenRoot(repo)
		if err != nil {
			return Reading{}, fmt.Errorf("reading the repository %s: %w", repo, err)
		}
		defer func() { _ = rr.Close() }()
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
