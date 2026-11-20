package contract

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Inferred is one value Pacto implied rather than read from pacto.yaml, with
// where it came from: a "<path>:<line>" inside the bundle, a
// "repository:<path>:<line>" in the repository outside the bundle (a
// CODEOWNERS file), "directory <name>" or "this CLI". In an inferred record it
// also carries its confidence (likely or possible), the YAML that would
// declare it, the class of file it was read from (ownerFile, for an owner
// read from CODEOWNERS or catalog-info.yaml; a source marker, never trust) and
// the sources of the same field it outranks.
type Inferred struct {
	Field      string   `json:"field" yaml:"field"`
	Value      string   `json:"value" yaml:"value"`
	Source     string   `json:"source" yaml:"source"`
	Confidence string   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Declare    string   `json:"declare,omitempty" yaml:"declare,omitempty"`
	Class      string   `json:"class,omitempty" yaml:"class,omitempty"`
	Outranks   []string `json:"outranks,omitempty" yaml:"outranks,omitempty"`
	// External is set on a dependencies[<name>] field that names a system
	// outside the cluster: its identity, sources and what else was read.
	External *InferredExternal `json:"external,omitempty" yaml:"external,omitempty"`
	// AsOf, Completeness and Proposed are hidden from the dashboard's OpenAPI
	// schema: only a record read from a cluster sets them, and the dashboard
	// carries a record's fields as fleet.InferredField, never as Inferred.
	//
	// AsOf is when a cluster fact was read (RFC 3339 UTC, seconds): set on
	// every field whose source is "cluster:<kind> ...".
	AsOf string `json:"asOf,omitempty" yaml:"asOf,omitempty" hidden:"true"`
	// Completeness is complete or partial on a runtime or fleet fact: partial
	// when a node, a pod or a supporting source went unobserved.
	Completeness string `json:"completeness,omitempty" yaml:"completeness,omitempty" hidden:"true"`
	// Proposed is a semver range proposed for a dependencies[<name>].compatibility
	// field: a proposal shown beside the declared value, never declared.
	Proposed string `json:"proposed,omitempty" yaml:"proposed,omitempty" hidden:"true"`
}

var (
	serviceNameRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	nameSeparatorRE = regexp.MustCompile(`[^a-z0-9]+`)
)

// SanitizeName turns s into a service name: lowercased, every run of other
// characters turned into one hyphen and the ends trimmed.
func SanitizeName(s string) string {
	return strings.Trim(nameSeparatorRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// maxInferFileSize bounds the files name inference reads.
const maxInferFileSize = 1 << 20

// nameCandidate is one name a file offers, at its depth in the bundle.
type nameCandidate struct {
	value, source string
	depth         int
}

// inferFiles is the bundle's files name inference reads, by kind.
type inferFiles struct {
	charts, kustomizations, composes, dockerfiles, yamls []string
}

// InferName infers service.name from fsys, the ignore-filtered bundle, in
// precedence order: a Chart.yaml name, a Kustomize metadata.name or the single
// Compose service, the org.opencontainers.image.title label of a Dockerfile, an
// app.kubernetes.io/name label in a manifest and finally dirName. A source
// counts only when it yields exactly one distinct valid service name.
func InferName(fsys fs.FS, dirName string) (Inferred, bool) {
	var files inferFiles
	if fsys != nil {
		files = scanInferFiles(fsys)
	}
	sources := []func() []nameCandidate{
		func() []nameCandidate { return shallowest(yamlNames(fsys, files.charts, chartName)) },
		func() []nameCandidate { return shallowest(yamlNames(fsys, files.kustomizations, metadataName)) },
		func() []nameCandidate { return yamlNames(fsys, files.composes, composeService) },
		func() []nameCandidate { return shallowest(dockerfileTitles(fsys, files.dockerfiles)) },
		func() []nameCandidate { return yamlNames(fsys, outsideCharts(files), nameLabel) },
	}
	for _, src := range sources {
		if c, ok := single(src()); ok {
			return Inferred{Field: "service.name", Value: c.value, Source: c.source}, true
		}
	}
	if name := SanitizeName(dirName); serviceNameRE.MatchString(name) {
		return Inferred{Field: "service.name", Value: name, Source: "directory " + dirName}, true
	}
	return Inferred{}, false
}

// scanInferFiles lists the files of fsys up to depth 3 that name inference
// reads, skipping hidden and vendored folders.
func scanInferFiles(fsys fs.FS) inferFiles {
	var f inferFiles
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback never fails
		depth := strings.Count(p, "/")
		if err != nil || p == "." {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); depth >= 3 || strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		f.add(p, d.Name(), depth)
		return nil
	})
	return f
}

// add files p, named name at depth, under the kind it is.
func (f *inferFiles) add(p, name string, depth int) {
	switch {
	case name == "Chart.yaml" && depth <= 2:
		f.charts = append(f.charts, p)
	case (name == "kustomization.yaml" || name == "kustomization.yml") && depth <= 1:
		f.kustomizations = append(f.kustomizations, p)
	case slices.Contains([]string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}, name) && depth == 0:
		f.composes = append(f.composes, p)
	case name == "Dockerfile":
		f.dockerfiles = append(f.dockerfiles, p)
	case strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"):
		f.yamls = append(f.yamls, p)
	}
}

// outsideCharts is every manifest that is not inside a Helm chart folder,
// whose templates are not YAML until rendered.
func outsideCharts(f inferFiles) []string {
	var out []string
	for _, p := range f.yamls {
		if !slices.ContainsFunc(f.charts, func(chart string) bool {
			dir := path.Dir(chart)
			return dir == "." || strings.HasPrefix(p, dir+"/")
		}) {
			out = append(out, p)
		}
	}
	return out
}

// ReadBounded reads p unless it cannot be opened, is not a regular file (a
// link, a FIFO or a device, checked on the opened file) or is over 1 MiB.
func ReadBounded(fsys fs.FS, p string) ([]byte, bool) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInferFileSize {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInferFileSize+1))
	return data, err == nil && len(data) <= maxInferFileSize
}

// yamlNames runs pick over every document of every file in paths.
func yamlNames(fsys fs.FS, paths []string, pick func(doc *yaml.Node) *yaml.Node) []nameCandidate {
	var out []nameCandidate
	for _, p := range paths {
		data, ok := ReadBounded(fsys, p)
		if !ok {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var doc yaml.Node
			if dec.Decode(&doc) != nil {
				break
			}
			if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
				if n := pick(doc.Content[0]); n != nil && n.Kind == yaml.ScalarNode {
					out = append(out, nameCandidate{value: n.Value, source: fmt.Sprintf("%s:%d", p, n.Line), depth: strings.Count(p, "/")})
				}
			}
		}
	}
	return out
}

// MapValue is the value under key in mapping m, or nil.
func MapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func chartName(doc *yaml.Node) *yaml.Node { return MapValue(doc, "name") }

func metadataName(doc *yaml.Node) *yaml.Node { return MapValue(MapValue(doc, "metadata"), "name") }

func nameLabel(doc *yaml.Node) *yaml.Node {
	return MapValue(MapValue(MapValue(doc, "metadata"), "labels"), "app.kubernetes.io/name")
}

// composeService is the key of the only service under services, or nil.
func composeService(doc *yaml.Node) *yaml.Node {
	if s := MapValue(doc, "services"); s != nil && s.Kind == yaml.MappingNode && len(s.Content) == 2 {
		return s.Content[0]
	}
	return nil
}

// dockerfileTitles reads the org.opencontainers.image.title LABEL of each
// Dockerfile, continuation lines and quoting included.
func dockerfileTitles(fsys fs.FS, paths []string) []nameCandidate {
	var out []nameCandidate
	for _, p := range paths {
		data, ok := ReadBounded(fsys, p)
		if !ok {
			continue
		}
		for _, ins := range DockerInstructions(data) {
			fields := strings.Fields(ins.Text)
			if len(fields) == 0 || !strings.EqualFold(fields[0], "LABEL") {
				continue
			}
			for _, tok := range LabelTokens(strings.TrimSpace(ins.Text[len(fields[0]):])) {
				if k, v, ok := strings.Cut(tok, "="); ok && k == "org.opencontainers.image.title" {
					out = append(out, nameCandidate{value: v, source: fmt.Sprintf("%s:%d", p, ins.Line), depth: strings.Count(p, "/")})
				}
			}
		}
	}
	return out
}

// DockerInstruction is one Dockerfile instruction with its continuation lines
// joined, and the line it starts on.
type DockerInstruction struct {
	Text string
	Line int
}

// DockerInstructions splits a Dockerfile into its instructions, comments and
// blank lines left out.
func DockerInstructions(data []byte) []DockerInstruction {
	var out []DockerInstruction
	var cur strings.Builder
	start := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if cur.Len() == 0 && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		if cur.Len() == 0 {
			start = n
		}
		if body, more := strings.CutSuffix(line, `\`); more {
			cur.WriteString(body + " ")
			continue
		}
		cur.WriteString(line)
		out = append(out, DockerInstruction{Text: cur.String(), Line: start})
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, DockerInstruction{Text: cur.String(), Line: start})
	}
	return out
}

// LabelTokens splits LABEL arguments on unquoted whitespace and drops the
// quotes, so `a=b "c"="d e"` is [a=b c=d e].
func LabelTokens(s string) []string {
	var out []string
	var tok strings.Builder
	var quote rune
	inTok := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			tok.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inTok = r, true
		case r == ' ' || r == '\t':
			if inTok {
				out, inTok = append(out, tok.String()), false
				tok.Reset()
			}
		default:
			tok.WriteRune(r)
			inTok = true
		}
	}
	if inTok {
		out = append(out, tok.String())
	}
	return out
}

// shallowest keeps the candidates at the smallest depth.
func shallowest(cs []nameCandidate) []nameCandidate {
	if len(cs) == 0 {
		return nil
	}
	lo := slices.MinFunc(cs, func(a, b nameCandidate) int { return cmp.Compare(a.depth, b.depth) }).depth
	return slices.DeleteFunc(cs, func(c nameCandidate) bool { return c.depth != lo })
}

// single is the first candidate when every candidate names the same valid
// service name.
func single(cs []nameCandidate) (nameCandidate, bool) {
	if len(cs) == 0 || !serviceNameRE.MatchString(cs[0].value) {
		return nameCandidate{}, false
	}
	for _, c := range cs[1:] {
		if c.value != cs[0].value {
			return nameCandidate{}, false
		}
	}
	return cs[0], true
}

// NameClaim is one folder or reference claiming a service name, and whether
// the name was inferred rather than declared.
type NameClaim struct {
	Dir, Name, Source string
	Inferred          bool
}

// Collision is a name claimed by two or more claims of which one or more is
// inferred.
type Collision struct {
	Name   string
	Claims []NameClaim
}

// NameCollisions groups claims by name and returns every name with two or more
// claims of which at least one is inferred, sorted by name with claims by Dir.
// Two declared claims are two revisions of one service, never a collision.
func NameCollisions(claims []NameClaim) []Collision {
	byName := map[string][]NameClaim{}
	for _, c := range claims {
		byName[c.Name] = append(byName[c.Name], c)
	}
	var out []Collision
	for name, cs := range byName {
		if len(cs) > 1 && slices.ContainsFunc(cs, func(c NameClaim) bool { return c.Inferred }) {
			slices.SortFunc(cs, func(a, b NameClaim) int { return cmp.Or(cmp.Compare(a.Dir, b.Dir), cmp.Compare(a.Source, b.Source)) })
			out = append(out, Collision{Name: name, Claims: cs})
		}
	}
	slices.SortFunc(out, func(a, b Collision) int { return cmp.Compare(a.Name, b.Name) })
	return out
}
