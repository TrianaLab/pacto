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
// asked and the draft are not. A surface's values are compared (diffSurface).
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
	changes = append(changes, diffSurface(oldRec, newRec)...)
	return redact(changes, sensitiveInferred(changes))
}

// diffSurface compares the surface values of two records, list by list: a
// value added or removed is a NON_BREAKING inferred.surface.<list> change.
// Sources, catalogs, times, identity, class, asOf and completeness are
// provenance and emit nothing, so a new caller, which adds only a source,
// changes nothing.
func diffSurface(oldRec, newRec *contract.InferredRecord) []Change {
	oldS, newS := surfaceOf(oldRec), surfaceOf(newRec)
	var changes []Change
	for _, l := range []struct {
		name     string
		old, new []contract.SurfaceValue
	}{{"hosts", oldS.Hosts, newS.Hosts}, {"ports", oldS.Ports, newS.Ports}, {"clients", oldS.Clients, newS.Clients}} {
		o, n := surfaceValues(l.old), surfaceValues(l.new)
		for _, v := range sortedUnion(o, n) {
			if o[v] != n[v] {
				changes = append(changes, valueChange(Change{Path: "inferred.surface." + l.name,
					Classification: NonBreaking, Reason: inferredChangeReason}, keep(o[v], v), keep(n[v], v)))
			}
		}
	}
	return changes
}

// surfaceOf is the record's surface, empty when there is none.
func surfaceOf(rec *contract.InferredRecord) contract.InferredSurface {
	if rec == nil || rec.Surface == nil {
		return contract.InferredSurface{}
	}
	return *rec.Surface
}

// surfaceValues is the set of values of one surface list.
func surfaceValues(vals []contract.SurfaceValue) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v.Value] = true
	}
	return m
}

// keep is v when present holds, empty otherwise.
func keep(present bool, v string) string {
	if present {
		return v
	}
	return ""
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
