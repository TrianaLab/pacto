package diff

import (
	"io/fs"
	"maps"
	"slices"
	"strconv"
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

// Advisory is a possible break on an interface only the inferred record
// names: the breaking findings of the same spec diff a declared interface
// gets. It never enters Result.Changes, so it never raises the
// classification, the derived version or a gate.
type Advisory struct {
	Interface string   `json:"interface" doc:"The inferred interface, as pacto.inferred.yaml names it (api, events or grpc)"`
	Kind      string   `json:"kind" doc:"The spec kind: openapi, asyncapi or grpc"`
	Source    string   `json:"source" doc:"Where Pacto found the spec, as path:line"`
	Declare   string   `json:"declare" doc:"The YAML that declares the interface in pacto.yaml, so the change is classified"`
	Changes   []Change `json:"changes" doc:"The breaking findings of the spec diff. Advisory: a possible break on an inferred interface, never part of the classification"`
}

// Summary is "<interface> <source>: <first path> <reason>", then
// " (+<n> more)" for the other findings.
func (a Advisory) Summary() string {
	out := a.Interface + " " + a.Source
	if len(a.Changes) > 0 {
		out += ": " + a.Changes[0].Path + " " + a.Changes[0].Reason
	}
	if n := len(a.Changes) - 1; n > 0 {
		out += " (+" + strconv.Itoa(n) + " more)"
	}
	return out
}

// inferredAdvisories are the possible breaks on the interfaces the new
// record names and new does not declare: for each, when the old record names
// the same interface with the same kind and both spec files read, the
// breaking findings of their spec diff. No old record, a missing or
// unreadable file, a kind change or no breaking finding gives nothing, and
// the change reads as it did (unknown is never positive).
func inferredAdvisories(new *contract.Contract, oldFS, newFS fs.FS) []Advisory {
	oldF, newF := inferredFields(inferredRecord(oldFS)), inferredFields(inferredRecord(newFS))
	var out []Advisory
	for _, name := range slices.Sorted(maps.Keys(newF)) {
		iface, ok := strings.CutPrefix(name, "interfaces[")
		iface = strings.TrimSuffix(iface, "]")
		if !ok || slices.ContainsFunc(new.Interfaces, func(i contract.Interface) bool { return i.Name == iface }) {
			continue
		}
		kind, newRef, _ := strings.Cut(newF[name].Value, " ")
		oldKind, oldRef, _ := strings.Cut(oldF[name].Value, " ")
		if oldKind != kind {
			continue
		}
		var breaking []Change
		for _, c := range specDiff(kind, oldRef, newRef, oldFS, newFS) {
			if c.Classification == Breaking {
				breaking = append(breaking, c)
			}
		}
		if len(breaking) > 0 {
			f := newF[name]
			out = append(out, Advisory{Interface: iface, Kind: kind, Source: f.Source, Declare: f.Declare, Changes: breaking})
		}
	}
	return out
}
