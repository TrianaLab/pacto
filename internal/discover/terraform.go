package discover

import (
	"cmp"
	"path"
	"regexp"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// Why a Terraform file, or a part of one, is not read.
const (
	unsupportedTerraformJSON  = "Terraform JSON configuration is not read; write it as .tf to have Pacto read it"
	unsupportedTerraformState = "Terraform state holds attribute values, which Pacto never reads from a folder"
)

// terraformFile reports whether the base name names a Terraform file the
// scan records: configuration it reads, and JSON configuration and state it
// names and never opens.
func terraformFile(base string) bool {
	for _, suffix := range []string{".tf", ".tf.json", ".tfstate", ".tfstate.backup"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// tfResource is a resource block: its type, name label, the literal of the
// catalog's id attribute and of region (empty when not a literal), whether it
// names its own provider and the line it starts on.
type tfResource struct {
	typ, name, id, region string
	ownProvider           bool
	line                  int
}

// tfProvider is a provider block: its name, the literal region and account
// (a single allowed_account_ids entry or a project) and whether it has an
// alias.
type tfProvider struct {
	name, region, account string
	aliased               bool
}

var (
	tfResourceRE = regexp.MustCompile(`^\s*resource\s+"([A-Za-z0-9_]+)"\s+"([A-Za-z_][A-Za-z0-9_-]*)"\s*\{`)
	tfProviderRE = regexp.MustCompile(`^\s*provider\s+"([A-Za-z0-9_-]+)"\s*\{`)
	tfModuleRE   = regexp.MustCompile(`^\s*module\s+"([A-Za-z_][A-Za-z0-9_-]*)"\s*\{`)
	tfAttrRE     = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(.*)$`)
	// tfLiteralRE is a quoted string with no interpolation or escape.
	tfLiteralRE = regexp.MustCompile(`^"([^"\\$%]*)"\s*(?:(?:#|//).*)?$`)
	tfOneListRE = regexp.MustCompile(`^\[\s*"([^"\\$%]*)"\s*,?\s*\]\s*(?:(?:#|//).*)?$`)
	heredocRE   = regexp.MustCompile(`^<<-?([A-Za-z_][A-Za-z0-9_]*)`)
)

// terraformResources reads the resource and provider blocks of one .tf file.
// idAttr is the attribute a resource type's catalog row names as its id,
// empty when none; it is read only when it is not a sensitive key. Only the
// id attribute, region, alias, provider and the account literals are looked
// at; every other attribute is skipped unread. A module block is a finding.
//
// ponytail: a line scanner, not HCL; a literal split over lines or built by a
// function is missed and the identity falls back (an honest partial one).
// Swap in hashicorp/hcl/v2 if real repositories need it.
func terraformResources(file string, data []byte, idAttr func(typ string) string) ([]tfResource, []tfProvider, []contract.InferredFinding) {
	t := &tfFile{file: file, idAttr: idAttr}
	eachLine(data, func(n int, line string) {
		if t.lex.heredoc == "" && !t.lex.comment {
			switch t.lex.depth {
			case 0:
				t.header(n, line)
			case 1:
				t.attribute(line)
			}
		}
		t.lex.line(line)
		if t.lex.depth == 0 {
			t.resource, t.provider = nil, nil
		}
	})
	return t.resources, t.providers, t.findings
}

// tfFile is the state of terraformResources over one file.
type tfFile struct {
	file      string
	idAttr    func(string) string
	lex       tfLexer
	resource  *tfResource // the resource block the scan is in
	provider  *tfProvider // the provider block the scan is in
	resources []tfResource
	providers []tfProvider
	findings  []contract.InferredFinding
}

// header reads a line at depth 0: the start of a resource, provider or
// module block.
func (t *tfFile) header(n int, line string) {
	if m := tfResourceRE.FindStringSubmatch(line); m != nil {
		t.resources = append(t.resources, tfResource{typ: m[1], name: m[2], line: n})
		t.resource = &t.resources[len(t.resources)-1]
	} else if m := tfProviderRE.FindStringSubmatch(line); m != nil {
		t.providers = append(t.providers, tfProvider{name: m[1]})
		t.provider = &t.providers[len(t.providers)-1]
	} else if m := tfModuleRE.FindStringSubmatch(line); m != nil {
		src := at(t.file, n)
		t.findings = append(t.findings, contract.InferredFinding{Code: CodeUnsupportedFile, Source: src,
			Message: src + " calls the Terraform module " + m[1] + ", which Pacto does not read"})
	}
}

// attribute reads a line at depth 1 of a resource or provider block, for the
// few attributes it looks at.
func (t *tfFile) attribute(line string) {
	m := tfAttrRE.FindStringSubmatch(line)
	if m == nil {
		return
	}
	name, value := m[1], strings.TrimSpace(m[2])
	switch {
	case t.resource != nil:
		r := t.resource
		switch name {
		case "provider":
			r.ownProvider = true
		case "region":
			r.region = tfLiteral(value)
		case t.idAttr(r.typ):
			if !schemax.SensitiveKey(name) { // whatever the catalog says
				r.id = tfLiteral(value)
			}
		}
	case t.provider != nil:
		p := t.provider
		switch name {
		case "alias":
			p.aliased = true
		case "region":
			p.region = tfLiteral(value)
		case "project":
			p.account = tfLiteral(value)
		case "allowed_account_ids":
			if l := tfOneListRE.FindStringSubmatch(value); l != nil {
				p.account = l[1]
			}
		}
	}
}

// tfLiteral is the text of a quoted literal, empty when value is anything
// else.
func tfLiteral(value string) string {
	if m := tfLiteralRE.FindStringSubmatch(value); m != nil {
		return m[1]
	}
	return ""
}

// tfLexer tracks the brace depth of HCL line by line outside strings,
// comments and heredocs. A quoted string never spans lines in HCL, so the
// strings and templates open on a line close with it.
type tfLexer struct {
	depth   int
	comment bool   // inside /* */
	heredoc string // the marker that ends the heredoc the line is in
	stack   []int  // open strings (-1) and templates (their open braces)
}

// line moves the lexer past one line.
func (l *tfLexer) line(s string) {
	if l.heredoc != "" {
		if strings.TrimSpace(s) == l.heredoc {
			l.heredoc = ""
		}
		return
	}
	l.stack = l.stack[:0]
	for i := 0; i < len(s); i++ {
		switch {
		case l.comment:
			if strings.HasPrefix(s[i:], "*/") {
				l.comment = false
				i++
			}
		case len(l.stack) > 0 && l.stack[len(l.stack)-1] < 0:
			i = l.inString(s, i)
		default:
			if i = l.inCode(s, i); i < 0 {
				return // a line comment
			}
		}
	}
}

// inString moves past the byte at i of a quoted string and returns where it
// ends.
func (l *tfLexer) inString(s string, i int) int {
	next := byte(0)
	if i+1 < len(s) {
		next = s[i+1]
	}
	switch c := s[i]; {
	case c == '\\', (c == '$' || c == '%') && next == c: // an escape, $${ or %%{
		return i + 1
	case c == '"':
		l.stack = l.stack[:len(l.stack)-1]
	case (c == '$' || c == '%') && next == '{':
		l.stack = append(l.stack, 0)
		return i + 1
	}
	return i
}

// inCode moves past the byte at i of code or of a template and returns where
// it ends, -1 when the rest of the line is a comment.
func (l *tfLexer) inCode(s string, i int) int {
	top := len(l.stack) - 1
	switch c := s[i]; {
	case c == '#' || strings.HasPrefix(s[i:], "//"):
		return -1
	case strings.HasPrefix(s[i:], "/*"):
		l.comment = true
		return i + 1
	case c == '"':
		l.stack = append(l.stack, -1)
	case c == '{' && top >= 0:
		l.stack[top]++
	case c == '{':
		l.depth++
	case c == '}':
		l.closeBrace(top)
	case top < 0 && heredocRE.MatchString(s[i:]):
		l.heredoc = heredocRE.FindStringSubmatch(s[i:])[1]
		return len(s)
	}
	return i
}

// closeBrace closes a brace of the template on top of the stack, the
// template itself when it has none open, or a block when no template is.
func (l *tfLexer) closeBrace(top int) {
	switch {
	case top >= 0 && l.stack[top] == 0:
		l.stack = l.stack[:top]
	case top >= 0:
		l.stack[top]--
	default:
		l.depth = max(l.depth-1, 0)
	}
}

// terraformUses are the managed cloud services the folder dir's .tf files
// provision, classified with cat, each only possible: a resource in the
// folder proves the team provisions it, not that this service calls it. A
// .tf.json file and Terraform state are findings and are never opened. It
// also returns every file it read.
func (sc *Scan) terraformUses(dir string, cat *externalcatalog.Catalog) ([]External, []contract.InferredFinding, []string) {
	u := &folderScan{dir: dir}
	idAttr := func(typ string) string { _, attr, _ := cat.MatchTerraform(typ); return attr }
	type module struct {
		resources []tfResource
		providers []tfProvider
		file      []string // the file of each resource
	}
	modules := map[string]*module{} // by folder: a Terraform module is one folder
	var order []string
	for _, rel := range sc.tfFiles {
		if why := terraformUnread(rel); why != "" {
			u.findings = append(u.findings, contract.InferredFinding{Code: CodeUnsupportedFile, Source: rel, Message: unsupportedMessage(rel, why)})
			continue
		}
		data, ok := u.readFile(rel)
		if !ok {
			continue
		}
		rs, ps, findings := terraformResources(rel, data, idAttr)
		u.findings = append(u.findings, findings...)
		m := modules[path.Dir(rel)]
		if m == nil {
			m = &module{}
			modules[path.Dir(rel)] = m
			order = append(order, path.Dir(rel))
		}
		m.resources, m.providers = append(m.resources, rs...), append(m.providers, ps...)
		for range rs {
			m.file = append(m.file, rel)
		}
	}
	for _, d := range order {
		m := modules[d]
		for i, r := range m.resources {
			if x, ok := r.external(cat, m.file[i], m.providers); ok {
				u.xs = append(u.xs, x)
			}
		}
	}
	return u.xs, u.findings, u.read
}

// terraformUnread is why the Terraform file rel is not opened, empty when it
// is read.
func terraformUnread(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".tf.json"):
		return unsupportedTerraformJSON
	case !strings.HasSuffix(rel, ".tf"):
		return unsupportedTerraformState
	}
	return ""
}

// external is the managed service the resource r in file provisions, with
// the region and account of its module's one provider block without an
// alias. A resource that names its own provider, several such blocks or a
// part that is not a literal leave that part unknown.
func (r tfResource) external(cat *externalcatalog.Catalog, file string, providers []tfProvider) (External, bool) {
	m, _, ok := cat.MatchTerraform(r.typ)
	if !ok {
		return External{}, false
	}
	prefix, _, _ := strings.Cut(r.typ, "_")
	var region, account string
	var plain []tfProvider
	for _, p := range providers {
		if p.name == prefix && !p.aliased {
			plain = append(plain, p)
		}
	}
	if len(plain) == 1 && !r.ownProvider {
		region, account = plain[0].region, plain[0].account
	}
	return managedExternal(m, managedSource{kind: "terraform", path: file, line: r.line, key: r.typ + "." + r.name,
		typ: r.typ, ids: []string{r.id}, label: r.name, account: account, region: cmp.Or(r.region, region)})
}

// managedSource is a managed resource a folder provisions: the kind, file,
// line and key of the source, the resource type, the candidate ids in
// order and the account and region read, empty when unknown. label names
// the resource when no id is read: a Terraform address, never its cloud id.
type managedSource struct {
	kind, path, key, typ string
	line                 int
	ids                  []string
	label                string
	account, region      string
}

// managedExternal is the external of the catalog match m for the resource s.
// The identity is the five-segment managed identity only when provider,
// account, region and id are all read; otherwise it is the catalog's
// identity and the resource names the parts that are missing, so an
// identity is never invented. The id is the first candidate that makes an
// identity segment; with none the label names the resource and the id is
// missing, and with no label either the resource is not recorded.
func managedExternal(m externalcatalog.Match, s managedSource) (External, bool) {
	var id string
	for _, c := range s.ids {
		if segment(c) {
			id = strings.ToLower(c)
			break
		}
	}
	res := &contract.InferredResource{Type: s.typ, Name: id}
	if id == "" && segment(s.label) {
		res.Name = strings.ToLower(s.label)
		res.Missing = append(res.Missing, "id")
	}
	if res.Name == "" {
		return External{}, false
	}
	account, region := strings.ToLower(s.account), strings.ToLower(s.region)
	if !segment(account) {
		res.Missing = append(res.Missing, "account")
	}
	if !segment(region) {
		res.Missing = append(res.Missing, "region")
	}
	identity := string(m.Identity)
	if provider, service, ok := strings.Cut(identity, "/"); ok && len(res.Missing) == 0 {
		full, _ := externalid.Managed(provider, service, account, region, id) // ponytail: every part is a segment
		identity = string(full)
	}
	key := s.key
	if !valueKeyRE.MatchString(key) {
		key = ""
	}
	where := at(s.path, s.line)
	if key != "" {
		where += " " + key
	}
	return External{Identity: identity, Class: m.Class, Confidence: m.Confidence, Entry: m.Entry, Origin: m.Origin,
		Kind: s.kind, Path: s.path, Line: s.line, Key: key, Where: commentSafe(where), Resource: res}, true
}

// segment reports whether s, lower-cased, is one identity segment.
func segment(s string) bool {
	if s == "" || strings.Contains(s, "/") {
		return false
	}
	_, err := externalid.Parse(strings.ToLower(s))
	return err == nil
}
