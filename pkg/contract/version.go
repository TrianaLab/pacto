package contract

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Known reports whether v is a pactoVersion this Pacto supports.
func Known(v string) bool { return slices.Contains(SupportedVersions, v) }

// Behind returns Current() and whether v is a supported pactoVersion older
// than it. An unknown or empty v is never behind.
func Behind(v string) (current string, behind bool) {
	i := slices.Index(SupportedVersions, v)
	return Current(), i >= 0 && i < len(SupportedVersions)-1
}

// ErrManualMigration is returned by Upgrade for a 1.x contract: moving it to
// 2.x changes its shape, which a person does by following MIGRATION.md.
type ErrManualMigration struct{ Version string }

// Error says why the version is not raised and where the migration guide is.
func (e *ErrManualMigration) Error() string {
	return fmt.Sprintf("pactoVersion %q is not migrated automatically: moving from 1.x to 2.x changes the contract's shape and needs a person; follow https://github.com/TrianaLab/pacto/blob/main/MIGRATION.md", e.Version)
}

// UpgradeCheck validates a pacto.yaml and returns its errors and warnings. An
// error means the check cannot tell what the contract means.
type UpgradeCheck func(raw []byte) ([]ValidationError, []ValidationWarning, error)

// Upgraded is the result of Upgrade. Raw is the raised document, or the
// original when the version was not raised. From is the declared version and
// To the version Raw declares. Kept, when set, is the finding the raised
// document has that the original lacks: raising would change its meaning.
type Upgraded struct {
	Raw      []byte
	From, To string
	Kept     *ValidationError
}

// Change is the one line every caller prints for an Upgrade: what was set, why
// the version was kept, or "" when nothing happened.
func (u Upgraded) Change() string {
	switch {
	case u.Kept != nil:
		return fmt.Sprintf("kept at %s: %s at %s: %s", u.From, u.Kept.Code, u.Kept.Path, u.Kept.Message)
	case u.To != u.From:
		return fmt.Sprintf("set pactoVersion to %q (was %q)", u.To, u.From)
	}
	return ""
}

// Upgrade raises raw's pactoVersion to to, editing only the version token so
// comments, key order, quoting and line endings survive. It runs check on the
// original and on the raised bytes and keeps the original when the raised one
// has a finding the original lacks (counted by severity, code and path); Kept
// names that finding. When neither run adds a finding, a check error keeps the
// original and is returned: an unknown result never raises a version.
//
// An absent pactoVersion, or one at or past to, returns raw unchanged without
// running check. A 1.x version returns *ErrManualMigration, and a version or
// target this Pacto does not know returns a *ParseError at pactoVersion.
func Upgrade(raw []byte, to string, check UpgradeCheck) (Upgraded, error) {
	n, err := versionNode(raw)
	if err != nil || n == nil {
		return Upgraded{Raw: raw}, err
	}
	from := n.Value
	kept := Upgraded{Raw: raw, From: from, To: from}
	switch {
	case from == "1" || strings.HasPrefix(from, "1."):
		return kept, &ErrManualMigration{Version: from}
	case !Known(to):
		return kept, &ParseError{Path: "pactoVersion", Message: fmt.Sprintf("cannot upgrade to unsupported pactoVersion %q", to)}
	case !Known(from):
		return kept, &ParseError{Path: "pactoVersion", Message: fmt.Sprintf("unsupported pactoVersion %q; supported: %s", from, strings.Join(SupportedVersions, ", "))}
	case slices.Index(SupportedVersions, from) >= slices.Index(SupportedVersions, to):
		return kept, nil
	}
	out, err := splice(raw, n, to)
	if err != nil {
		return kept, err
	}
	e0, w0, err0 := check(raw)
	e1, w1, err1 := check(out)
	if k := newFinding(e0, w0, e1, w1); k != nil {
		kept.Kept = k
		return kept, nil
	}
	if err0 != nil {
		return kept, err0
	}
	if err1 != nil {
		return kept, err1
	}
	return Upgraded{Raw: out, From: from, To: to}, nil
}

// versionNode is the pactoVersion value node of raw's first document, or nil
// when the document does not declare one. A value that is not a plain or
// quoted scalar is an error: it is edited by hand.
func versionNode(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, &ParseError{Message: fmt.Sprintf("failed to parse YAML: %v", err), Err: err}
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &ParseError{Message: "the document is not a mapping; edit pactoVersion by hand"}
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "pactoVersion" {
			continue
		}
		v := root.Content[i+1]
		if v.Kind != yaml.ScalarNode || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return nil, &ParseError{Path: "pactoVersion", Message: "pactoVersion is not a plain or quoted scalar; edit pactoVersion by hand"}
		}
		return v, nil
	}
	return nil, nil
}

// splice replaces n's token in raw with to, in the same quoting style. It
// refuses when the bytes at n's position are not the token its value implies,
// as for an escaped double-quoted value.
func splice(raw []byte, n *yaml.Node, to string) ([]byte, error) {
	quote := ""
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		quote = `"`
	case n.Style&yaml.SingleQuotedStyle != 0:
		quote = "'"
	}
	off := offsetOf(raw, n.Line, n.Column)
	token := []byte(quote + n.Value + quote)
	if off < 0 || !bytes.HasPrefix(raw[off:], token) {
		return nil, &ParseError{Path: "pactoVersion", Message: "pactoVersion is not written as a simple value; edit pactoVersion by hand"}
	}
	out := make([]byte, 0, len(raw)+len(to)-len(n.Value))
	out = append(out, raw[:off]...)
	out = append(out, quote+to+quote...)
	return append(out, raw[off+len(token):]...), nil
}

// offsetOf is the byte offset of the 1-based line and column (in characters)
// in raw, or -1 when raw has no such position.
func offsetOf(raw []byte, line, col int) int {
	off := 0
	for range line - 1 {
		i := bytes.IndexByte(raw[off:], '\n')
		if i < 0 {
			return -1
		}
		off += i + 1
	}
	for range col - 1 {
		if off >= len(raw) || raw[off] == '\n' {
			return -1
		}
		_, w := utf8.DecodeRune(raw[off:])
		off += w
	}
	return off
}

// findingKey is what makes two findings the same for Upgrade.
type findingKey struct {
	err        bool
	code, path string
}

// newFinding is the first finding of the raised run (errors, then warnings)
// whose severity, code and path occur more often than in the original run, or
// nil when there is none.
func newFinding(e0 []ValidationError, w0 []ValidationWarning, e1 []ValidationError, w1 []ValidationWarning) *ValidationError {
	seen := map[findingKey]int{}
	for _, e := range e0 {
		seen[findingKey{true, e.Code, e.Path}]++
	}
	for _, w := range w0 {
		seen[findingKey{false, w.Code, w.Path}]++
	}
	for _, e := range e1 {
		k := findingKey{true, e.Code, e.Path}
		if seen[k]--; seen[k] < 0 {
			return &e
		}
	}
	for _, w := range w1 {
		k := findingKey{false, w.Code, w.Path}
		if seen[k]--; seen[k] < 0 {
			return &ValidationError{Path: w.Path, Code: w.Code, Message: w.Message}
		}
	}
	return nil
}
