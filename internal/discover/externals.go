package discover

import (
	"cmp"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// External is one place the folder names a system outside the cluster: a
// vendor API, a managed cloud service or a legacy host. It carries the kind
// of source, the file, line and key, and the host and port at most, never the
// value it was read from. Where is the same place on one printable line.
type External struct {
	Identity   string `json:"identity"`
	Class      string `json:"class"`
	Confidence string `json:"confidence"`
	Where      string `json:"where"`
	Host       string `json:"host,omitempty"`
	Entry      string `json:"entry,omitempty"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Line       int    `json:"line,omitempty"`
	Key        string `json:"key,omitempty"`
	Origin     string `json:"origin,omitempty"`
	// SDK and Major are the client library an sdk source lists, as written,
	// and its major, nil when unknown.
	SDK   string `json:"-"`
	Major *int   `json:"-"`
	// Resource is the managed resource a terraform or crossplane source
	// provisions, with the identity parts it could not read.
	Resource *contract.InferredResource `json:"-"`
}

// valueKeyRE is a key a value is read under: an environment or values key,
// never a URL or a line with no '='.
var valueKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// value hands the string value at line of the file p, under key, to the
// Values visitor. A key that is not a key name or is sensitive is never
// handed over, so its value is never parsed.
func (r *reader) value(kind, p string, line int, key, value string) {
	if r.in.Values != nil && valueKeyRE.MatchString(key) && !schemax.SensitiveKey(key) {
		r.in.Values(Value{Kind: kind, Path: p, Line: line, Key: key, Value: value})
	}
}

// keyValue hands a KEY=VALUE line to the Values visitor; a line with no '='
// is not read.
func (r *reader) keyValue(kind, p string, line int, kv string) {
	if key, value, ok := strings.Cut(kv, "="); ok {
		r.value(kind, p, line, key, value)
	}
}

// externals classifies, with cat, the workload's environment (a Secret
// contributes its name and key names, never a value) and every value the
// readers handed over. A value that names a host outside the cluster no
// catalog entry identifies is a finding.
func (sc *Scan) externals(values []Value, cat *externalcatalog.Catalog) ([]External, []contract.InferredFinding) {
	var out []External
	var findings []contract.InferredFinding
	add := func(v Value, ms []externalcatalog.Match) {
		if !valueKeyRE.MatchString(v.Key) {
			v.Key = ""
		}
		if !recordable(v.Path) {
			return
		}
		for _, m := range ms {
			x := External{Identity: string(m.Identity), Class: m.Class, Confidence: m.Confidence, Host: m.Host, Entry: m.Entry,
				Kind: v.Kind, Path: v.Path, Line: v.Line, Key: v.Key, Origin: m.Origin, Where: v.where()}
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	classify := func(v Value) {
		ms := cat.Classify(v.Value, v.Key)
		add(v, ms)
		if recordable(v.Path) && !schemax.SensitiveKey(v.Key) && namesHost(externalcatalog.NormalizeHost(v.Value)) &&
			!slices.ContainsFunc(ms, func(m externalcatalog.Match) bool { return m.Host != "" }) {
			findings = append(findings, contract.InferredFinding{Code: CodeUnresolved, Source: v.source(), Message: unidentifiedHostMessage(v.where())})
		}
	}
	envValues, names := sc.envValues()
	for _, v := range names {
		add(v, cat.Classify("", v.Key))
		add(v, cat.Classify("", v.Value))
	}
	for _, v := range append(envValues, values...) {
		classify(v)
	}
	slices.SortStableFunc(out, func(a, b External) int {
		return cmp.Or(strings.Compare(a.Identity, b.Identity), strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), strings.Compare(a.Key, b.Key))
	})
	return out, slices.Compact(findings)
}

// recordable reports whether a record source can name the path p.
func recordable(p string) bool { return fs.ValidPath(p) && !strings.ContainsAny(p, ":?#") }

// envValues are the workload's environment: the values to classify (env and
// ConfigMap) and the names alone, never a Secret's value: a secretKeyRef's
// variable with the Secret key name as its Value, and an envFrom
// secretRef's Secret name as its Key, since its keys live in the Secret.
func (sc *Scan) envValues() (values, names []Value) {
	for _, e := range sc.Env {
		file, line := splitLine(e.Source)
		v := Value{Kind: "env", Path: cmp.Or(file, e.File), Line: line, Key: e.Name, Value: e.Value}
		switch {
		case e.Secret && e.Key != "":
			v.Kind, v.Value = "secretKeyRef", e.Key
			names = append(names, v)
			continue
		case e.From == "configmap":
			v.Kind = "configMap"
		}
		values = append(values, v)
	}
	if sc.Workload != nil {
		for _, e := range sc.Workload.Env {
			if e.From == "secret" && e.Key == "" {
				file, line := splitLine(e.Source)
				names = append(names, Value{Kind: "secretRef", Path: cmp.Or(file, e.File), Line: line, Key: e.Ref})
			}
		}
	}
	return values, names
}

// source is the "<path>[:<line>]" of v.
func (v Value) source() string {
	if v.Line == 0 {
		return v.Path
	}
	return at(v.Path, v.Line)
}

// where is "<path>[:<line>] <KEY>", safe to print on one line.
func (v Value) where() string { return commentSafe(v.source() + " " + v.Key) }

// unidentifiedHostMessage says the value at where names a host no catalog
// entry identifies.
func unidentifiedHostMessage(where string) string {
	return where + " names a host outside the cluster that no catalog entry identifies: declare it in pacto.yaml if the service calls it"
}

// externalFields are the record fields of xs: one dependencies[<name>] per
// identity (per identity and resource name for a managed resource, named by
// the resource and carrying it), its value the identity, its source the first place that names
// it with its confidence, the strongest, and every place in External.Sources.
// taken reports a field another reader already gives, so the name falls back
// to the whole identity; declared reports an external the contract declares,
// which is left out. cited gets every file a source names. An external
// listed by client libraries of several majors is also a finding.
func externalFields(xs []External, taken func(field string) bool, declared func(name, identity string) bool, cited map[string]bool) ([]contract.Inferred, []contract.InferredFinding) {
	byID := map[string][]External{} // by identity, then resource name
	for _, x := range xs {
		k := x.Identity
		if x.Resource != nil {
			k += "/" + x.Resource.Name
		}
		byID[k] = append(byID[k], x)
	}
	var out []contract.Inferred
	var findings []contract.InferredFinding
	for _, k := range slices.Sorted(maps.Keys(byID)) {
		group := byID[k]
		id, name := group[0].Identity, DependencyName(group[0].Identity)
		if r := group[0].Resource; r != nil {
			name = contract.SanitizeName(r.Name)
		}
		if taken("dependencies[" + name + "]") {
			// ponytail: one fallback; two identities sanitizing alike would need a counter.
			name = contract.SanitizeName(k)
		}
		if declared(name, id) {
			continue
		}
		best := group[0]
		if i := slices.IndexFunc(group, func(x External) bool { return x.Confidence == externalcatalog.ConfidenceLikely }); i >= 0 {
			best = group[i]
		}
		x := &contract.InferredExternal{Identity: id, Catalog: best.Origin, Resource: group[0].Resource}
		var finding *contract.InferredFinding
		if x.Client, finding = clientOf(id, group); finding != nil {
			findings = append(findings, *finding)
		}
		for _, g := range group {
			s := contract.InferredExternalSource{Kind: g.Kind, Path: g.Path, Line: g.Line, Key: g.Key, Host: g.Host}
			if !slices.Contains(x.Sources, s) {
				x.Sources = append(x.Sources, s)
			}
			cited[g.Path] = true
		}
		first := Value{Path: best.Path, Line: best.Line}
		out = append(out, contract.Inferred{Field: "dependencies[" + name + "]", Value: id, Source: first.source(), Confidence: best.Confidence,
			Declare: externalYAML(name, id), External: x})
	}
	return out, findings
}

// clientOf is the client library the sdk sources in group list for the
// external id, the one of the highest major, nil when none does. Several
// majors make a finding that names each with its file.
func clientOf(id string, group []External) (*contract.InferredClient, *contract.InferredFinding) {
	sdks := slices.DeleteFunc(slices.Clone(group), func(x External) bool { return x.Kind != "sdk" })
	if len(sdks) == 0 {
		return nil, nil
	}
	major := func(x External) int {
		if x.Major == nil {
			return -1
		}
		return *x.Major
	}
	slices.SortStableFunc(sdks, func(a, b External) int { return cmp.Compare(major(a), major(b)) })
	best := sdks[len(sdks)-1]
	c := &contract.InferredClient{SDK: best.SDK, Major: best.Major}
	var versions []string
	last := -1
	for _, s := range sdks {
		if m := major(s); m > last {
			versions, last = append(versions, fmt.Sprintf("%d (%s)", m, s.Path)), m
		}
	}
	if len(versions) < 2 {
		return c, nil
	}
	return c, &contract.InferredFinding{Code: CodeAmbiguous, Source: best.source(),
		Message: fmt.Sprintf("the folder names %d client versions of %s: %s; the record keeps the highest", len(versions), id, strings.Join(versions, ", "))}
}

// source is the "<path>[:<line>]" of x.
func (x External) source() string { return Value{Path: x.Path, Line: x.Line}.source() }

// externalYAML declares the external id under the dependency name.
func externalYAML(name, id string) string {
	return fmt.Sprintf("dependencies:\n  - name: %s\n    external: true\n    identity: %s\n    required: true\n    version: <the API version you call>\n", name, id)
}

// DependencyName is the identity when it is a valid dependency name, else
// its last component made one.
func DependencyName(id string) string {
	if dependencyNameRE.MatchString(id) {
		return id
	}
	return contract.SanitizeName(id[strings.LastIndex(id, "/")+1:])
}

var dependencyNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// CatalogInputs are the catalogs cat was built from, as "<origin> <digest>":
// what a record names as its catalog inputs.
func CatalogInputs(cat *externalcatalog.Catalog) []string {
	var out []string
	for _, r := range cat.Refs() {
		out = append(out, r.Origin+" "+r.Digest)
	}
	return out
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
