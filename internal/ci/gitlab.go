package ci

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// AddGitLabInclude is src, a .gitlab-ci.yml, with the pacto-actions
// component entry for url: appended as a new include key when there is none,
// put first in a block include list, or replacing the URL of an entry that
// already names a pacto-actions commit (how a GitLab pin moves, since
// Dependabot reads no GitLab file). Every byte outside the edit stays: the
// YAML is parsed only to find where the edit goes. An include written any
// other way is refused with the entry to add by hand.
func AddGitLabInclude(src []byte, url string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf(".gitlab-ci.yml: %w", err)
	}
	entry := "  - remote: " + url
	if len(doc.Content) == 0 {
		return appendInclude(src, entry), nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New(".gitlab-ci.yml: the top level is not a mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "include" {
			return editInclude(src, root.Content[i+1], url, entry)
		}
	}
	return appendInclude(src, entry), nil
}

// appendInclude is src with a new include key holding entry at its end.
func appendInclude(src []byte, entry string) []byte {
	out := slices.Clone(src)
	if len(out) > 0 {
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	return append(out, "include:\n"+entry+"\n"...)
}

// editInclude puts url into the include list n of src: in place of an
// existing pacto-actions URL, else as a new first item at its column.
func editInclude(src []byte, n *yaml.Node, url, entry string) ([]byte, error) {
	refuse := fmt.Errorf(".gitlab-ci.yml: include is not a block list pacto ci init can edit; add this entry to it:\n%s", entry)
	if n.Kind != yaml.SequenceNode || n.Style&yaml.FlowStyle != 0 || len(n.Content) == 0 {
		return nil, refuse
	}
	for _, item := range n.Content {
		if v := pactoActionsURL(item); v != nil {
			off := offset(src, v.Line, v.Column)
			i := bytes.Index(src[off:], []byte(v.Value))
			if i < 0 { // an escaped or folded scalar: its text is not its value
				return nil, refuse
			}
			off += i
			return slices.Concat(src[:off], []byte(url), src[off+len(v.Value):]), nil
		}
	}
	first := n.Content[0]
	start := offset(src, first.Line, 1)
	dash := bytes.LastIndexByte(src[start:offset(src, first.Line, first.Column)], '-')
	if dash < 0 { // the item starts on a line after its dash
		return nil, refuse
	}
	return slices.Concat(src[:start+dash], []byte("- remote: "+url+"\n"), src[start:]), nil
}

// pactoActionsURL is the scalar of an include item that names a pacto-actions
// commit, as a bare URL or under remote, or nil.
func pactoActionsURL(item *yaml.Node) *yaml.Node {
	if item.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "remote" {
				item = item.Content[i+1]
				break
			}
		}
	}
	if item.Kind == yaml.ScalarNode && strings.HasPrefix(item.Value, "https://raw.githubusercontent.com/"+ActionsRepo+"/") {
		return item
	}
	return nil
}

// offset is the byte offset in src of a yaml.v3 position: line and column
// both start at 1, and the column counts characters.
func offset(src []byte, line, col int) int {
	off := 0
	for ; line > 1; line-- {
		off += bytes.IndexByte(src[off:], '\n') + 1
	}
	for ; col > 1; col-- {
		_, w := utf8.DecodeRune(src[off:])
		off += w
	}
	return off
}
