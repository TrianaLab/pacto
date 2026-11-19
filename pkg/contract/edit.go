package contract

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetServiceVersion returns raw with service.version set to v: a declared
// version is replaced in place, on its own line, so comments stay; an
// undeclared one is inserted as PinServiceVersion inserts it.
func SetServiceVersion(raw []byte, v string) ([]byte, error) {
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("set service.version: %w", err)
	}
	n := mappingPath(top, "service", "version")
	if n == nil || n.Kind != yaml.ScalarNode {
		return PinServiceVersion(raw, v)
	}
	return replaceScalar(root, n, bom, body, v, n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0), nil
}

// SetCompatibility returns raw with the compatibility of the dependency name
// replaced by rng, on its own line, so the comment beside it stays. Only a
// declared compatibility: unknown is replaced.
func SetCompatibility(raw []byte, name, rng string) ([]byte, error) {
	return replaceCompatibility(raw, name, CompatibilityUnknown, rng)
}

// UnsetCompatibility returns raw with the compatibility rng of the
// dependency name back to unknown, on its own line. Only a declared rng is
// replaced.
func UnsetCompatibility(raw []byte, name, rng string) ([]byte, error) {
	return replaceCompatibility(raw, name, rng, CompatibilityUnknown)
}

// replaceCompatibility replaces the dependency name's compatibility from
// with to, quoted unless it is unknown.
func replaceCompatibility(raw []byte, name, from, to string) ([]byte, error) {
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("set the compatibility of %s: %w", name, err)
	}
	var n *yaml.Node
	if deps := mappingPath(top, "dependencies"); deps != nil && deps.Kind == yaml.SequenceNode {
		for _, d := range deps.Content {
			if d.Kind == yaml.MappingNode && scalarOf(d, "name") == name {
				n = valueOf(d, "compatibility")
			}
		}
	}
	if n == nil || n.Kind != yaml.ScalarNode || n.Value != from {
		return nil, fmt.Errorf("pacto.yaml declares no dependency %s with compatibility: %s", name, from)
	}
	return replaceScalar(root, n, bom, body, to, to != CompatibilityUnknown), nil
}

// RemoveServiceOwner returns raw without service.owner: the lines of a block
// owner are deleted, so the other lines and their comments stay. raw without
// an owner is returned as is.
func RemoveServiceOwner(raw []byte) ([]byte, error) {
	bom, body, root, top, err := decodeRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("remove service.owner: %w", err)
	}
	svc := mappingPath(top, "service")
	if svc == nil || svc.Kind != yaml.MappingNode {
		return raw, nil
	}
	i := slices.IndexFunc(svc.Content, func(k *yaml.Node) bool { return k.Value == "owner" })
	if i < 0 || i%2 != 0 {
		return raw, nil
	}
	if plainBlock(top) && plainBlock(svc) {
		lines := strings.SplitAfter(string(body), "\n")
		from, to := svc.Content[i].Line, lastLine(svc.Content[i+1])
		return slices.Concat(bom, []byte(strings.Join(lines[:from-1], "")), []byte(strings.Join(lines[to:], ""))), nil
	}
	svc.Content = slices.Delete(svc.Content, i, i+2)
	return reencode(root, bom, body), nil
}

// mappingPath is the node at the keys under the mapping top, nil when any
// is absent or top is not a mapping.
func mappingPath(top *yaml.Node, keys ...string) *yaml.Node {
	n := top
	for _, k := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		n = valueOf(n, k)
	}
	return n
}

// scalarOf is the scalar value of key in the mapping m, "" when absent.
func scalarOf(m *yaml.Node, key string) string {
	if n := valueOf(m, key); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// lastLine is the last line a node and its children start on.
func lastLine(n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		last = max(last, lastLine(c))
	}
	return last
}

// replaceScalar replaces the scalar n with text, double-quoted when quote,
// in its own line of body when the line holds n as written; otherwise the
// whole of root is re-encoded.
func replaceScalar(root, n *yaml.Node, bom, body []byte, text string, quote bool) []byte {
	written := n.Value
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		written = `"` + n.Value + `"`
	case n.Style&yaml.SingleQuotedStyle != 0:
		written = `'` + n.Value + `'`
	}
	with := text
	if quote {
		with = `"` + text + `"`
	}
	lines := strings.SplitAfter(string(body), "\n")
	if l, col := n.Line-1, n.Column-1; l < len(lines) && col <= len(lines[l]) && strings.HasPrefix(lines[l][col:], written) {
		lines[l] = lines[l][:col] + with + lines[l][col+len(written):]
		return slices.Concat(bom, []byte(strings.Join(lines, "")))
	}
	n.Value, n.Style = text, 0
	if quote {
		n.Style = yaml.DoubleQuotedStyle
	}
	return reencode(root, bom, body)
}
