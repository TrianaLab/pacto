package contract

import (
	"bytes"
	"errors"
	"fmt"
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
		return insertVersionLine(raw, bom, body, 0), nil
	case top.Kind == yaml.MappingNode && top.Style&yaml.FlowStyle == 0:
		if hasKey(top, "pactoVersion") {
			return raw, nil
		}
		return insertVersionLine(raw, bom, body, top.Content[0].Column-1), nil
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
func insertVersionLine(raw, bom, body []byte, indent int) []byte {
	nl := newline(body)
	line := strings.Repeat(" ", indent) + `pactoVersion: "` + Current() + `"` + nl
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
	out := make([]byte, 0, len(raw)+len(line))
	out = append(out, bom...)
	out = append(out, body[:at]...)
	out = append(out, line...)
	return append(out, body[at:]...)
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
