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
// missing FS, a missing record or an unreadable one compares as empty. A
// proposed range and a range the declarer accepted are compared too; when a
// cluster fact was read, how completely, the coverage, hidden counts, who
// asked and the draft are not.
func diffInferred(oldFS, newFS fs.FS) []Change {
	oldRec, newRec := inferredRecord(oldFS), inferredRecord(newFS)
	oldF, newF := inferredFields(oldRec), inferredFields(newRec)
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
		case o.Proposed != n.Proposed:
			c.Path += ".proposed"
			c = valueChange(c, o.Proposed, n.Proposed)
		default:
			continue
		}
		changes = append(changes, c)
	}
	oldA, newA := acceptedRanges(oldRec), acceptedRanges(newRec)
	for _, field := range sortedUnion(oldA, newA) {
		if o, n := oldA[field], newA[field]; o != n {
			changes = append(changes, valueChange(Change{Path: "inferred.declaration.accepted." + field,
				Classification: NonBreaking, Reason: inferredChangeReason}, o, n))
		}
	}
	return redact(changes, sensitiveInferred(changes))
}

// valueChange is c adding after, removing before or changing one into the
// other, as either is empty.
func valueChange(c Change, before, after string) Change {
	switch {
	case before == "":
		c.Type, c.NewValue = Added, after
	case after == "":
		c.Type, c.OldValue = Removed, before
	default:
		c.Type, c.OldValue, c.NewValue = Modified, before, after
	}
	return c
}

// inferredRecord is the record fsys holds, nil when there is none or it
// cannot be read.
func inferredRecord(fsys fs.FS) *contract.InferredRecord {
	rec, err := contract.ReadInferredRecord(fsys)
	if err != nil {
		return nil
	}
	return rec
}

// inferredFields is the record's fields by name, empty when there is none.
func inferredFields(rec *contract.InferredRecord) map[string]contract.Inferred {
	if rec == nil {
		return nil
	}
	m := make(map[string]contract.Inferred, len(rec.Fields))
	for _, f := range rec.Fields {
		m[f.Field] = f
	}
	return m
}

// acceptedRanges is the range the record's declarer accepted for each
// field, empty when it declares nothing.
func acceptedRanges(rec *contract.InferredRecord) map[string]string {
	if rec == nil || rec.Declaration == nil {
		return nil
	}
	m := make(map[string]string, len(rec.Declaration.Accepted))
	for _, a := range rec.Declaration.Accepted {
		m[a.Field] = a.Range
	}
	return m
}

func sortedUnion[V any](a, b map[string]V) []string {
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
