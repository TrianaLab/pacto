package pactify

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// External is a system outside the cluster that the folder names: a vendor
// API, a managed cloud service or a legacy host. Host is the host and port at
// most, never the value it was read from.
type External struct {
	Identity   string `json:"identity"`
	Class      string `json:"class"`
	Confidence string `json:"confidence"`
	Where      string `json:"where"`
	Host       string `json:"host,omitempty"`
	Entry      string `json:"entry,omitempty"`
}

// catalog is the catalog externals are classified with and its digest, with
// " (embedded)" when it is the copy compiled into this binary.
func (p *planner) catalog() (*externalcatalog.Catalog, string) {
	if c := p.opts.Catalog; c != nil {
		return c, c.Refs()[0].Digest
	}
	return externalcatalog.Default(), externalcatalog.DefaultDigest() + " (embedded)"
}

// externals reads the externals the folder names and says what it found:
// a likely one in Found, a possible one (a key name with nothing to confirm
// it) and a host outside the cluster that nothing names in Unknown.
func (p *planner) externals(dir string) {
	cat, digest := p.catalog()
	p.r.Catalog = digest
	p.r.Externals = p.sc.externals(dir, cat)
	likely := map[string]bool{}
	for _, e := range p.r.Externals {
		if e.Confidence == externalcatalog.ConfidenceLikely {
			likely[e.Identity] = true
			p.found("%s calls %s (%s, %s)", p.r.Service, e.Identity, e.Host, e.Where)
		}
	}
	for _, e := range p.r.Externals {
		if !likely[e.Identity] {
			p.unknown("%s suggests %s: nothing else in the folder confirms it", e.Where, e.Identity)
		}
	}
	// A value under a sensitive key is never parsed, so it never gets here.
	unidentified := func(where, value, key string) {
		if !schemax.SensitiveKey(key) && namesHost(externalcatalog.NormalizeHost(value)) && !slices.ContainsFunc(p.r.Externals, func(x External) bool { return x.Host != "" && x.Where == where }) {
			p.unknown("%s names a host outside the cluster that no catalog entry identifies: declare it in pacto.yaml if %s calls it", where, p.r.Service)
		}
	}
	for _, e := range p.sc.env {
		if !e.secret {
			unidentified(envWhere(e), e.value, e.name)
		}
	}
	for _, c := range p.sc.charts {
		p.sc.chartValues(dir, c, unidentified)
	}
}

func envWhere(e envVar) string { return commentSafe(e.file + ", env " + e.name) }

// externals classifies the workload's environment and every chart's
// values.yaml. A Secret contributes its name and key names, never a value.
func (sc *scan) externals(dir string, cat *externalcatalog.Catalog) []External {
	out := []External{}
	add := func(where string, ms []externalcatalog.Match) {
		for _, m := range ms {
			x := External{Identity: string(m.Identity), Class: m.Class, Confidence: m.Confidence, Where: commentSafe(where), Host: m.Host, Entry: m.Entry}
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	for _, e := range sc.env {
		if e.secret && e.key != "" {
			where := fmt.Sprintf("%s, secretKeyRef %s/%s (key name only)", e.file, e.ref, e.key)
			add(where, cat.Classify("", e.name))
			add(where, cat.Classify("", e.key))
			continue
		}
		add(envWhere(e), cat.Classify(e.value, e.name))
	}
	// A secretRef never reaches sc.env: its keys live in the Secret.
	if sc.workload != nil {
		for _, e := range sc.workload.env {
			if e.from == "secret" && e.key == "" {
				add(fmt.Sprintf("%s, secretRef %s (Secret name only)", e.file, e.ref), cat.Classify("", e.ref))
			}
		}
	}
	for _, c := range sc.charts {
		sc.chartValues(dir, c, func(where, value, key string) { add(where, cat.Classify(value, key)) })
	}
	slices.SortStableFunc(out, func(a, b External) int {
		return strings.Compare(a.Identity+"\x00"+a.Where, b.Identity+"\x00"+b.Where)
	})
	return out
}

// chartValues calls visit with every string leaf of a chart's values.yaml,
// read as data: templates are never rendered. A values.yaml .pactoignore
// leaves out is not read. where has its non-printable characters replaced.
func (sc *scan) chartValues(dir, chart string, visit func(where, value, key string)) {
	file := path.Join(chart, "values.yaml")
	if sc.ign != nil && sc.ign.Ignored(file, false) {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, chart, "values.yaml"))
	if err != nil {
		return
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return
	}
	stringLeaves(&doc, "", func(n *yaml.Node, key string) {
		visit(commentSafe(fmt.Sprintf("%s:%d %s", file, n.Line, key)), n.Value, key)
	})
}

// stringLeaves calls visit with every string scalar under n and the key of
// the mapping entry it is in. Aliases are not followed.
func stringLeaves(n *yaml.Node, key string, visit func(*yaml.Node, string)) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			stringLeaves(n.Content[i+1], n.Content[i].Value, visit)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" {
			visit(n, key)
		}
	default: // documents and sequences
		for _, c := range n.Content {
			stringLeaves(c, key, visit)
		}
	}
}

// namesHost reports whether a value is a URL or host:port of a name outside
// the cluster: one with a scheme or a port.
func namesHost(hs []externalcatalog.HostPort) bool {
	return slices.ContainsFunc(hs, func(h externalcatalog.HostPort) bool { return h.Scheme || h.Port != "" })
}

// externalEntries writes the likely externals as a commented block under
// dependencies, so nothing inferred becomes declared until a person
// uncomments it. Every interpolated string has its control characters
// replaced, so no value can end a comment line.
func (p *planner) externalEntries(b *strings.Builder) {
	var likely []External
	for _, e := range p.r.Externals {
		if e.Confidence == externalcatalog.ConfidenceLikely && !slices.ContainsFunc(likely, func(x External) bool { return x.Identity == e.Identity }) {
			likely = append(likely, e)
		}
	}
	if len(likely) == 0 {
		return
	}
	if !strings.Contains(b.String(), "\ndependencies:\n") {
		b.WriteString("# dependencies:\n")
	}
	b.WriteString("  # inferred externals: uncomment the ones this service calls and set the version\n")
	for _, e := range likely {
		name, id := dependencyName(e.Identity), commentSafe(e.Identity)
		detail := "likely, catalog " + commentSafe(p.r.Catalog)
		if e.Entry != "" {
			detail += ", entry " + commentSafe(e.Entry)
		} else {
			detail += ", " + commentSafe(e.Class)
		}
		if name != id {
			detail += ", identity " + id
		}
		fmt.Fprintf(b, "  # - name: %s  # %s (%s)\n  #   external: true\n  #   required: true\n  #   version: <the API version you call>\n",
			name, commentSafe(e.Where), detail)
	}
	p.next("pacto.yaml lists the externals the folder names as comments under dependencies: uncomment the ones %s calls and set the version it calls", p.r.Service)
}

// dependencyName is the identity when it is a valid dependency name, else its
// last component made one.
func dependencyName(id string) string {
	if nameRE.MatchString(id) {
		return id
	}
	last := id[strings.LastIndex(id, "/")+1:]
	return strings.Trim(nonNameRE.ReplaceAllString(strings.ToLower(last), "-"), "-")
}

// commentSafe replaces every rune that is not printable on one line with
// "?": control characters, and U+2028 and U+2029, which YAML also reads as
// line breaks.
func commentSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, s)
}
