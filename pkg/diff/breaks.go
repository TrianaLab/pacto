package diff

import (
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffBreaks compares the breaks two versions announce, each keyed by its
// sorted consumers joined with ",". Every change is NonBreaking: an
// announcement changes no interface.
func diffBreaks(old, new []contract.Break) []Change {
	var changes []Change
	oldByKey, newByKey := indexBreaks(old), indexBreaks(new)
	for _, k := range slices.Sorted(maps.Keys(oldByKey)) {
		o := oldByKey[k]
		n, ok := newByKey[k]
		if !ok {
			changes = append(changes, newChange("breaks", Removed, k, nil))
			continue
		}
		if o.Reason != n.Reason {
			changes = append(changes, newChange("breaks.reason", Modified, k+": "+o.Reason, k+": "+n.Reason))
		}
		if o.Expires != n.Expires {
			changes = append(changes, newChange("breaks.expires", strChangeType(o.Expires, n.Expires), k+": "+o.Expires, k+": "+n.Expires))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(newByKey)) {
		if _, ok := oldByKey[k]; !ok {
			changes = append(changes, newChange("breaks", Added, nil, k))
		}
	}
	return changes
}

func indexBreaks(breaks []contract.Break) map[string]contract.Break {
	m := make(map[string]contract.Break, len(breaks))
	for _, b := range breaks {
		m[strings.Join(slices.Sorted(slices.Values(b.Consumers)), ",")] = b
	}
	return m
}
