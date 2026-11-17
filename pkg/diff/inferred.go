package diff

import (
	"io/fs"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

const inferredChangeReason = "inferred by Pacto, not part of the declared contract"

// diffInferred compares the inferred records of two bundles field by field.
// Every change is NON_BREAKING and built here, never through the rule table:
// the record is information beside the declared contract, so an inferred-only
// diff never asks more than a patch. Inputs are provenance and emit nothing; a
// missing FS, a missing record or an unreadable one compares as empty.
func diffInferred(oldFS, newFS fs.FS) []Change {
	oldF, newF := inferredFields(oldFS), inferredFields(newFS)
	var changes []Change
	for _, name := range sortedUnion(oldF, newF) {
		o, inOld := oldF[name]
		n, inNew := newF[name]
		c := Change{Path: "inferred." + name, Classification: NonBreaking, Reason: inferredChangeReason}
		switch {
		case !inOld:
			c.Type, c.NewValue = Added, n.Value
		case !inNew:
			c.Type, c.OldValue = Removed, o.Value
		case o.Value != n.Value:
			c.Type, c.OldValue, c.NewValue = Modified, o.Value, n.Value
		case o.Source != n.Source:
			c.Type, c.OldValue, c.NewValue = Modified, o.Source, n.Source
		default:
			continue
		}
		changes = append(changes, c)
	}
	return redact(changes, sensitiveInferred(changes))
}

// inferredFields is the record's fields by name, empty when there is none.
func inferredFields(fsys fs.FS) map[string]contract.Inferred {
	rec, err := contract.ReadInferredRecord(fsys)
	if err != nil || rec == nil {
		return nil
	}
	m := make(map[string]contract.Inferred, len(rec.Fields))
	for _, f := range rec.Fields {
		m[f.Field] = f
	}
	return m
}

func sortedUnion(a, b map[string]contract.Inferred) []string {
	var keys []string
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// sensitiveInferred is the path of every inferred configuration key whose
// name marks it sensitive. A record never holds a configuration value, only
// its type, but it goes through the same mask as every configuration diff.
func sensitiveInferred(changes []Change) []string {
	var paths []string
	for _, c := range changes {
		if key, ok := strings.CutPrefix(c.Path, "inferred.configuration."); ok && schemax.SensitiveKey(key) {
			paths = append(paths, c.Path)
		}
	}
	return paths
}
