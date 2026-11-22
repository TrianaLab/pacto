package contract

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const utf8BOM = "\ufeff"

// PinVersion returns raw with `pactoVersion: "<Current()>"` declared at the
// root, so a published bundle always carries the version it was read as. The
// key is looked up on the decoded root mapping, never textually: a commented
// or indented pactoVersion is not a declaration. A declared key leaves raw
// unchanged. A block mapping (or an empty document) gets one line inserted at
// the top, after a leading `---` or directive header, keeping the BOM and the
// line endings; a flow mapping (JSON included) or an explicit null gets the
// pair prepended and is re-encoded.
func PinVersion(raw []byte) ([]byte, error) {
	body, hasBOM := bytes.CutPrefix(raw, []byte(utf8BOM))
	var bom []byte
	if hasBOM {
		bom = []byte(utf8BOM)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("pin pactoVersion: %w", err)
	}
	var top *yaml.Node
	if root.Kind == yaml.DocumentNode {
		top = root.Content[0]
	}
	switch {
	case top == nil || (top.Tag == "!!null" && top.Value == ""):
		return insertVersionLine(bom, body, 0), nil
	// A root tag or anchor (`--- !!map`, `&root`) sits on a line of its own
	// that a text insert could split from its mapping, so it is re-encoded.
	case top.Kind == yaml.MappingNode && top.Style&(yaml.FlowStyle|yaml.TaggedStyle) == 0 && top.Anchor == "":
		if hasKey(top, "pactoVersion") {
			return raw, nil
		}
		return insertVersionLine(bom, body, top.Content[0].Column-1), nil
	case top.Kind == yaml.MappingNode:
		if hasKey(top, "pactoVersion") {
			return raw, nil
		}
	case top.Tag == "!!null":
		top.Kind, top.Tag, top.Value, top.Style = yaml.MappingNode, "!!map", "", 0
	default:
		return nil, errors.New("pin pactoVersion: the contract is not a mapping")
	}
	top.Content = append([]*yaml.Node{versionKey(), versionValue()}, top.Content...)
	return reencode(&root, bom, body), nil
}

func hasKey(m *yaml.Node, key string) bool {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}

func versionKey() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "pactoVersion"}
}

func versionValue() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: Current(), Style: yaml.DoubleQuotedStyle}
}

// newline is the line ending body uses.
func newline(body []byte) string {
	if bytes.Contains(body, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// insertVersionLine inserts the pactoVersion line, indented by indent
// columns, after the leading directives and document marker of body, or at the
// top when there is none.
func insertVersionLine(bom, body []byte, indent int) []byte {
	return insertAtTop(bom, body, strings.Repeat(" ", indent)+`pactoVersion: "`+Current()+`"`)
}

// insertAtTop inserts text, one or more lines without their last line
// ending, after the leading directives and document marker of body, or at the
// top when there is none, with body's line endings.
func insertAtTop(bom, body []byte, text string) []byte {
	nl := newline(body)
	line := strings.ReplaceAll(text, "\n", nl) + nl
	at, off := 0, 0
	for _, l := range strings.SplitAfter(string(body), "\n") {
		off += len(l)
		t := strings.TrimSpace(l)
		if t == "---" || strings.HasPrefix(t, "--- #") {
			at = off
			break
		}
		if t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "%") {
			break
		}
	}
	if at == len(body) && at > 0 && !strings.HasSuffix(string(body), "\n") {
		line = nl + line
	}
	return slices.Concat(bom, body[:at], []byte(line), body[at:])
}

// reencode writes root back as YAML with the BOM and line endings of body.
func reencode(root *yaml.Node, bom, body []byte) []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(root) // a tree yaml.v3 decoded, plus two scalars, always encodes
	_ = enc.Close()
	out := buf.Bytes()
	if nl := newline(body); nl != "\n" {
		out = bytes.ReplaceAll(out, []byte("\n"), []byte(nl))
	}
	return append(append([]byte{}, bom...), out...)
}

// PinServiceVersion returns raw with service.version declared as v, so a
// published bundle carries the version Pacto derived. A declared version
// leaves raw unchanged. A block service mapping gets one line inserted above
// its first key, at its indent; a contract with no service key gets the two
// lines at the top. Anything else (a flow, tagged or anchored mapping, an
// empty service) is re-encoded, keeping the BOM and the line endings.
func PinServiceVersion(raw []byte, v string) ([]byte, error) {
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("pin service.version: %w", err)
	}
	if top == nil || (top.Tag == "!!null" && top.Value == "") {
		return insertAtTop(bom, body, "service:\n  version: "+v), nil
	}
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("pin service.version: the contract is not a mapping")
	}
	svc := valueOf(top, "service")
	if svc != nil && svc.Kind == yaml.MappingNode && hasKey(svc, "version") {
		return raw, nil
	}
	if plainBlock(top) {
		indent := strings.Repeat(" ", top.Content[0].Column-1)
		switch {
		case svc == nil:
			return insertAtTop(bom, body, indent+"service:\n"+indent+"  version: "+v), nil
		case svc.Kind == yaml.MappingNode && plainBlock(svc):
			return insertBeforeLine(bom, body, svc.Content[0].Line, strings.Repeat(" ", svc.Content[0].Column-1)+"version: "+v), nil
		}
	}
	if err := addServiceVersion(top, svc, v); err != nil {
		return nil, err
	}
	return reencode(root, bom, body), nil
}

// decodeRoot splits raw into its BOM and body and decodes the body: the
// document node and its root (nil for an empty document).
func decodeRoot(raw []byte) (bom, body []byte, root, top *yaml.Node, err error) {
	body, hasBOM := bytes.CutPrefix(raw, []byte(utf8BOM))
	if hasBOM {
		bom = []byte(utf8BOM)
	}
	root = &yaml.Node{}
	if err := yaml.Unmarshal(body, root); err != nil {
		return nil, nil, nil, nil, err
	}
	if root.Kind == yaml.DocumentNode {
		top = root.Content[0]
	}
	return bom, body, root, top, nil
}

// addServiceVersion adds version v to the service mapping svc of top,
// creating it when svc is nil or null.
func addServiceVersion(top, svc *yaml.Node, v string) error {
	version := []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: v}}
	switch {
	case svc == nil:
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "service"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: version})
	case svc.Kind == yaml.MappingNode:
		svc.Content = append(svc.Content, version...)
	case svc.Tag == "!!null":
		svc.Kind, svc.Tag, svc.Value, svc.Style, svc.Content = yaml.MappingNode, "!!map", "", 0, version
	default:
		return errors.New("pin service.version: service is not a mapping")
	}
	return nil
}

// valueOf is the value of key in the mapping m, nil when absent.
func valueOf(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// plainBlock reports whether m is a non-empty block mapping with no tag or
// anchor, so a line inserted at its indent stays inside it.
func plainBlock(m *yaml.Node) bool {
	return len(m.Content) > 0 && m.Style&(yaml.FlowStyle|yaml.TaggedStyle) == 0 && m.Anchor == ""
}

// insertBeforeLine inserts text as a line above the 1-based line of body.
func insertBeforeLine(bom, body []byte, line int, text string) []byte {
	lines := strings.SplitAfter(string(body), "\n")
	at := len(strings.Join(lines[:line-1], ""))
	return slices.Concat(bom, body[:at], []byte(text+newline(body)), body[at:])
}

// PinDependencyField returns raw with key declared as value on the
// dependency called name, so an accepted field publishes as declared. A
// dependency that already declares key leaves raw unchanged: the
// repository wins. compatibility: unknown declares no range, so it is
// replaced in place, keeping the rest of the text. A block item whose first key sits on one line gets one
// line inserted below it, at its indent; anything else is re-encoded,
// keeping the BOM and the line endings. A contract with no dependency name
// is an error.
func PinDependencyField(raw []byte, name, key, value string) ([]byte, error) {
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("pin dependencies[%s].%s: %w", name, key, err)
	}
	item := dependencyItem(top, name)
	if item == nil {
		return nil, fmt.Errorf("pin dependencies[%s].%s: the contract declares no dependency %s", name, key, name)
	}
	quoted := strconv.Quote(value) // every Go escape is a YAML double-quoted escape
	if v := valueOf(item, key); v != nil {
		if key != "compatibility" || v.Kind != yaml.ScalarNode || v.Value != CompatibilityUnknown || value == CompatibilityUnknown {
			return raw, nil
		}
		return replaceUnknown(root, bom, body, v, value, quoted), nil
	}
	if first, v := item.Content[0], item.Content[1]; plainBlock(item) && v.Line == first.Line && v.Kind == yaml.ScalarNode &&
		v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 && bytes.HasSuffix(body, []byte("\n")) {
		return insertBeforeLine(bom, body, first.Line+1, strings.Repeat(" ", first.Column-1)+key+": "+quoted), nil
	}
	item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle})
	return reencode(root, bom, body), nil
}

// replaceUnknown returns body with the scalar v replaced by quoted where its
// text is plain or quoted on its own line; anything else is re-encoded with
// v set to value.
func replaceUnknown(root *yaml.Node, bom, body []byte, v *yaml.Node, value, quoted string) []byte {
	lines := strings.SplitAfter(string(body), "\n")
	text := v.Value
	if v.Style&yaml.DoubleQuotedStyle != 0 {
		text = `"` + text + `"`
	} else if v.Style&yaml.SingleQuotedStyle != 0 {
		text = "'" + text + "'"
	}
	// ponytail: Column counts runes; a line with a multi-byte rune before the
	// value misses the text and is re-encoded.
	at := len(strings.Join(lines[:v.Line-1], "")) + v.Column - 1
	if at+len(text) <= len(body) && string(body[at:at+len(text)]) == text {
		return slices.Concat(bom, body[:at], []byte(quoted), body[at+len(text):])
	}
	v.Value, v.Style, v.Tag = value, yaml.DoubleQuotedStyle, "!!str"
	return reencode(root, bom, body)
}

// dependencyItem is the mapping of the dependency called name under top's
// dependencies, nil when there is none.
func dependencyItem(top *yaml.Node, name string) *yaml.Node {
	if top == nil || top.Kind != yaml.MappingNode {
		return nil
	}
	deps := valueOf(top, "dependencies")
	if deps == nil || deps.Kind != yaml.SequenceNode {
		return nil
	}
	for _, it := range deps.Content {
		if n := valueOf(it, "name"); it.Kind == yaml.MappingNode && n != nil && n.Value == name {
			return it
		}
	}
	return nil
}

// Declares reports whether raw writes the record key field: service.owner,
// dependencies[<name>] or dependencies[<name>].compatibility. Only a key in
// the YAML counts: a default the parser fills (compatibility: unknown on a
// dependency with no range) is never written, an explicit one is.
func Declares(raw []byte, field string) bool {
	_, _, _, top, err := decodeRoot(raw)
	if err != nil || top == nil || top.Kind != yaml.MappingNode {
		return false
	}
	if field == "service.owner" {
		svc := valueOf(top, "service")
		return svc != nil && svc.Kind == yaml.MappingNode && hasKey(svc, "owner")
	}
	rest, ok := strings.CutPrefix(field, "dependencies[")
	name, key, _ := strings.Cut(rest, "]")
	item := dependencyItem(top, name)
	switch {
	case !ok || item == nil:
		return false
	case key == "":
		return true
	}
	return key == ".compatibility" && hasKey(item, "compatibility")
}

// PinServiceOwner returns raw with service.owner declared as o, so a
// carried owner publishes as declared. A block service mapping gets the
// owner block inserted above its first key, at its indent; a contract with
// no service key gets it at the top; anything else is re-encoded. A
// contract that already declares an owner is an error: the repository wins.
func PinServiceOwner(raw []byte, o Owner) ([]byte, error) {
	if Declares(raw, "service.owner") {
		return nil, errors.New("pin service.owner: pacto.yaml declares service.owner")
	}
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("pin service.owner: %w", err)
	}
	var block bytes.Buffer
	enc := yaml.NewEncoder(&block)
	enc.SetIndent(2)
	_ = enc.Encode(map[string]Owner{"owner": o}) // ponytail: an Owner always encodes
	text := strings.TrimSuffix(block.String(), "\n")
	if top == nil || (top.Tag == "!!null" && top.Value == "") {
		return insertAtTop(bom, body, "service:\n"+indentLines(text, "  ")), nil
	}
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("pin service.owner: the contract is not a mapping")
	}
	svc := valueOf(top, "service")
	if plainBlock(top) {
		indent := strings.Repeat(" ", top.Content[0].Column-1)
		switch {
		case svc == nil:
			return insertAtTop(bom, body, indent+"service:\n"+indentLines(text, indent+"  ")), nil
		case svc.Kind == yaml.MappingNode && plainBlock(svc):
			return insertBeforeLine(bom, body, svc.Content[0].Line, indentLines(text, strings.Repeat(" ", svc.Content[0].Column-1))), nil
		}
	}
	var owner yaml.Node
	_ = owner.Encode(o)
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "owner"}
	switch {
	case svc == nil:
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "service"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{key, &owner}})
	case svc.Kind == yaml.MappingNode:
		svc.Content = append(svc.Content, key, &owner)
	default:
		return nil, errors.New("pin service.owner: service is not a mapping")
	}
	return reencode(root, bom, body), nil
}

// AddDependency returns raw with d appended to dependencies as its name,
// its ref when set and its compatibility, creating the sequence when
// absent. A plain block contract with no dependencies key gets the lines at
// its end; anything else is re-encoded. A dependency of that name already
// declared is an error: the repository wins.
func AddDependency(raw []byte, d Dependency) ([]byte, error) {
	if Declares(raw, "dependencies["+d.Name+"]") {
		return nil, fmt.Errorf("add dependencies[%s]: pacto.yaml declares it", d.Name)
	}
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("add dependencies[%s]: %w", d.Name, err)
	}
	if top == nil || top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("add dependencies[%s]: the contract is not a mapping", d.Name)
	}
	lines := []string{"- name: " + strconv.Quote(d.Name)}
	if d.Ref != "" {
		lines = append(lines, "  ref: "+strconv.Quote(d.Ref))
	}
	lines = append(lines, "  compatibility: "+strconv.Quote(d.Compatibility))
	deps := valueOf(top, "dependencies")
	if deps == nil && plainBlock(top) && bytes.HasSuffix(body, []byte("\n")) {
		indent := strings.Repeat(" ", top.Content[0].Column-1)
		text := indent + "dependencies:\n" + indentLines(strings.Join(lines, "\n"), indent+"  ") + "\n"
		return slices.Concat(bom, body, []byte(strings.ReplaceAll(text, "\n", newline(body)))), nil
	}
	var item yaml.Node
	_ = yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &item) // ponytail: quoted scalars always decode
	switch {
	case deps == nil:
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "dependencies"}, item.Content[0])
	case deps.Kind == yaml.SequenceNode:
		deps.Content = append(deps.Content, item.Content[0].Content...)
	default:
		return nil, fmt.Errorf("add dependencies[%s]: dependencies is not a sequence", d.Name)
	}
	return reencode(root, bom, body), nil
}

// indentLines prefixes every line of text with indent.
func indentLines(text, indent string) string {
	return indent + strings.ReplaceAll(text, "\n", "\n"+indent)
}
