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
// repository wins. A block item whose first key sits on one line gets one
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
	if hasKey(item, key) {
		return raw, nil
	}
	quoted := strconv.Quote(value) // every Go escape is a YAML double-quoted escape
	if first, v := item.Content[0], item.Content[1]; plainBlock(item) && v.Line == first.Line && v.Kind == yaml.ScalarNode &&
		v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 && bytes.HasSuffix(body, []byte("\n")) {
		return insertBeforeLine(bom, body, first.Line+1, strings.Repeat(" ", first.Column-1)+key+": "+quoted), nil
	}
	item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle})
	return reencode(root, bom, body), nil
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
