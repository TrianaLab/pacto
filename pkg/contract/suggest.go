package contract

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DidYouMean returns ` (did you mean "x"?)` naming the candidate closest to
// got, or "" when none is close enough to be a likely typo.
func DidYouMean(got string, candidates []string) string {
	if got == "" {
		return ""
	}
	best, bestDist := "", max(2, len(got)/3)+1
	for _, c := range slices.Sorted(slices.Values(candidates)) {
		if d := editDistance(got, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

// editDistance is the Levenshtein distance between a and b, by byte.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

var unknownFieldRe = regexp.MustCompile(`field (\S+) not found in type (\S+)`)

// contractTypes maps each struct type reachable from Contract by its Go name
// (contract.State), the name yaml.v3 puts in an unknown-field error.
var contractTypes = func() map[string]reflect.Type {
	m := map[string]reflect.Type{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || m[t.String()] != nil {
			return
		}
		m[t.String()] = t
		for i := range t.NumField() {
			walk(t.Field(i).Type)
		}
	}
	walk(reflect.TypeFor[Contract]())
	return m
}()

// suggestFields appends the closest known field to each unknown-field error
// in a yaml.v3 decode message.
func suggestFields(msg string) string {
	return unknownFieldRe.ReplaceAllStringFunc(msg, func(m string) string {
		sub := unknownFieldRe.FindStringSubmatch(m)
		t, ok := contractTypes[sub[2]]
		if !ok {
			return m
		}
		fields, _ := structFields(t)
		var names []string
		for name := range fields {
			names = append(names, name)
		}
		return m + DidYouMean(sub[1], names)
	})
}

var pathIndexRe = regexp.MustCompile(`\[([^]]*)\]`)

// Locate returns the 1-based line and column in raw of the deepest node on
// path, a contract path written either way validation writes one
// (dependencies[0].ref or dependencies.0.ref). It returns 0, 0 when raw does
// not parse or no part of path is found.
func Locate(raw []byte, path string) (line, column int) {
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil || len(root.Content) == 0 || path == "" {
		return 0, 0
	}
	n := root.Content[0]
	for _, part := range strings.Split(pathIndexRe.ReplaceAllString(path, ".$1"), ".") {
		next := child(n, part)
		if next == nil {
			break
		}
		line, column, n = next.key.Line, next.key.Column, next.value
	}
	return line, column
}

type located struct{ key, value *yaml.Node }

// child is the entry of mapping or sequence n named by part, or nil.
func child(n *yaml.Node, part string) *located {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if n.Content[i-1].Value == part {
				return &located{n.Content[i-1], n.Content[i]}
			}
		}
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(part); err == nil && i >= 0 && i < len(n.Content) {
			return &located{n.Content[i], n.Content[i]}
		}
	}
	return nil
}
