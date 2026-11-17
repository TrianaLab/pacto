package discover

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

// EnvWhere is where an environment variable is set, safe to print on one line.
func EnvWhere(e EnvVar) string { return CommentSafe(e.File + ", env " + e.Name) }

// Externals classifies the workload's environment and every chart's
// values.yaml. A Secret contributes its name and key names, never a value.
func (sc *Scan) Externals(dir string, cat *externalcatalog.Catalog) []External {
	out := []External{}
	add := func(where string, ms []externalcatalog.Match) {
		for _, m := range ms {
			x := External{Identity: string(m.Identity), Class: m.Class, Confidence: m.Confidence, Where: CommentSafe(where), Host: m.Host, Entry: m.Entry}
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	for _, e := range sc.Env {
		if e.Secret && e.Key != "" {
			where := fmt.Sprintf("%s, secretKeyRef %s/%s (key name only)", e.File, e.Ref, e.Key)
			add(where, cat.Classify("", e.Name))
			add(where, cat.Classify("", e.Key))
			continue
		}
		add(EnvWhere(e), cat.Classify(e.Value, e.Name))
	}
	// A secretRef never reaches sc.env: its keys live in the Secret.
	if sc.Workload != nil {
		for _, e := range sc.Workload.Env {
			if e.From == "secret" && e.Key == "" {
				add(fmt.Sprintf("%s, secretRef %s (Secret name only)", e.File, e.Ref), cat.Classify("", e.Ref))
			}
		}
	}
	for _, c := range sc.Charts {
		sc.ChartValues(dir, c, func(where, value, key string) { add(where, cat.Classify(value, key)) })
	}
	slices.SortStableFunc(out, func(a, b External) int {
		return strings.Compare(a.Identity+"\x00"+a.Where, b.Identity+"\x00"+b.Where)
	})
	return out
}

// ChartValues calls visit with every string leaf of a chart's values.yaml,
// read as data: templates are never rendered. A values.yaml .pactoignore
// leaves out is not read. where has its non-printable characters replaced.
func (sc *Scan) ChartValues(dir, chart string, visit func(where, value, key string)) {
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
		visit(CommentSafe(fmt.Sprintf("%s:%d %s", file, n.Line, key)), n.Value, key)
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

// NamesHost reports whether a value is a URL or host:port of a name outside
// the cluster: one with a scheme or a port.
func NamesHost(hs []externalcatalog.HostPort) bool {
	return slices.ContainsFunc(hs, func(h externalcatalog.HostPort) bool { return h.Scheme || h.Port != "" })
}

// CommentSafe replaces every rune that is not printable on one line with
// "?": control characters, and U+2028 and U+2029, which YAML also reads as
// line breaks.
func CommentSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, s)
}
