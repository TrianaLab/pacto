package discover

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A chart's templates are scanned line by line, never rendered: a
// containerPort, an image and the names in an env block are read when their
// value is a literal or a reference values.yaml or Chart.yaml resolves.
// Anything else is one finding per template, naming its first such line and
// never the expression, which can hold a value.
// ponytail: line scan with a dotted-path resolver; a template parser when rendering arrives (decision 26).

// templateLineRE is a "key: value" line: its indent, an optional list dash,
// the key and the value.
var templateLineRE = regexp.MustCompile(`^( *)(- +)?([A-Za-z]+):(?:\s+(.*?))?\s*$`)

// templateExprRE is one {{ }} action, with its trim markers, and its body.
var templateExprRE = regexp.MustCompile(`\{\{-?\s*(.*?)\s*-?\}\}`)

// chartTemplates scans each template of the chart in dir.
func (r *reader) chartTemplates(dir string) {
	prefix := path.Join(dir, "templates") + "/"
	values, chart := r.chartFiles[path.Join(dir, "values.yaml")], r.chartFiles[path.Join(dir, "Chart.yaml")]
	for _, p := range r.templates {
		if strings.HasPrefix(p, prefix) {
			if data := r.read(p); data != nil {
				r.template(p, string(data), templateScope{values, chart})
			}
		}
	}
}

// templateScope is what a template reference resolves against.
type templateScope struct{ values, chart *yaml.Node }

// envBlock tracks the env block a template scan is in.
type envBlock struct{ at, item int } // the env key's column, -1 outside one; its items' column, -1 before the first

// template scans the template p, text.
func (r *reader) template(p, text string, s templateScope) {
	env := envBlock{at: -1, item: -1}
	unresolved := 0
	for i, line := range strings.Split(text, "\n") {
		env.end(line)
		if !r.templateLine(p, i+1, line, s, &env) && unresolved == 0 {
			unresolved = i + 1
		}
	}
	if unresolved > 0 {
		src := fmt.Sprintf("%s:%d", p, unresolved)
		r.find(CodeUnresolved, src, unresolvedTemplateMessage(src))
	}
}

// end leaves the env block at a line indented at or left of its key, unless
// it is one of its items; a line of only template actions keeps it open.
func (b *envBlock) end(line string) {
	trimmed := strings.TrimLeft(line, " ")
	if b.at < 0 || trimmed == "" || strings.HasPrefix(trimmed, "{{") {
		return
	}
	if indent := len(line) - len(trimmed); indent < b.at || (indent == b.at && !strings.HasPrefix(trimmed, "-")) {
		b.at = -1
	}
}

// templateLine reads one line of the template p, false when it sets a value
// Pacto cannot resolve.
func (r *reader) templateLine(p string, n int, line string, s templateScope, env *envBlock) bool {
	m := templateLineRE.FindStringSubmatch(line)
	if m == nil {
		return true
	}
	column, key := len(m[1])+len(m[2]), m[3]
	if env.at >= 0 && m[2] != "" && key == "name" {
		if env.item < 0 {
			env.item = len(m[1])
		}
		if len(m[1]) != env.item {
			return true
		}
		name, ok := s.resolve(m[4])
		if ok {
			r.addKey(p, n, rankTemplate, name)
		}
		return ok
	}
	switch {
	case key == "env" && m[4] == "":
		*env = envBlock{at: column, item: -1}
	case key == "containerPort":
		v, ok := s.resolve(m[4])
		if port, proto, isPort := exposedPort(v); ok && isPort {
			r.addAt(p, n, rankTemplate, fmt.Sprintf("ports[%d]", port), fmt.Sprintf("%d/%s", port, proto), "")
		}
		return ok
	case key == "image":
		v, ok := s.resolve(m[4])
		if ok {
			r.addAt(p, n, rankTemplate, "image", v, "")
		}
		return ok
	}
	return true
}

// resolve is the value v with every action resolved, ok false when one does
// not resolve or the value is empty.
func (s templateScope) resolve(v string) (string, bool) {
	ok := true
	out := templateExprRE.ReplaceAllStringFunc(strings.Trim(v, `"'`), func(action string) string {
		got, found := s.expr(templateExprRE.FindStringSubmatch(action)[1])
		ok = ok && found
		return got
	})
	return out, ok && out != "" && !strings.Contains(out, "{{")
}

// expr resolves one action body: a .Values path, .Chart.Name or
// .Chart.AppVersion, with an optional "| default .Chart.AppVersion".
func (s templateScope) expr(body string) (string, bool) {
	ref, def, hasDefault := strings.Cut(body, "|")
	if hasDefault && strings.TrimSpace(def) != "default .Chart.AppVersion" {
		return "", false
	}
	got := s.lookup(strings.TrimSpace(ref))
	if got == "" && hasDefault {
		got = scalar(contract.MapValue(s.chart, "appVersion"))
	}
	return got, got != ""
}

// lookup is the scalar ref names, "" when it names none.
func (s templateScope) lookup(ref string) string {
	switch ref {
	case ".Chart.Name":
		return scalar(contract.MapValue(s.chart, "name"))
	case ".Chart.AppVersion":
		return scalar(contract.MapValue(s.chart, "appVersion"))
	}
	keys, ok := strings.CutPrefix(ref, ".Values.")
	if !ok {
		return ""
	}
	n := s.values
	for k := range strings.SplitSeq(keys, ".") {
		n = contract.MapValue(n, k)
	}
	return scalar(n)
}
