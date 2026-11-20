package detect

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// PullSecret proposes the pull Secret the most workloads that pull from the
// registry entry reg's host name in their pod specs, as <namespace>/<name>,
// ties broken by name. It is inferred from pod specs only: Pacto never reads
// the Secret, and nothing uses it until a person sets registryPullSecret. The
// hint appears only when reg has one (a clear majority). No pod naming a pull
// Secret is no entry (an empty Kind).
func PullSecret(ws []Workload, reg Entry, clusterWide bool) Entry {
	host, _, _ := strings.Cut(reg.Value, "/")
	t := tallies{}
	for _, w := range ws {
		if !slices.ContainsFunc(w.Images, func(img string) bool { return imageHost(img) == host }) {
			continue
		}
		seen := map[string]bool{}
		for _, n := range w.PullSecrets {
			if id := w.Namespace + "/" + n; n != "" && !seen[id] {
				seen[id] = true
				t.add(id, "")
			}
		}
	}
	if host == "" || len(t) == 0 {
		return Entry{}
	}
	top := slices.MinFunc(slices.Collect(maps.Values(t)), func(a, b *tally) int { return cmp.Or(cmp.Compare(b.n, a.n), strings.Compare(a.name, b.name)) })
	ns, _, _ := strings.Cut(top.name, "/")
	plural := "s"
	if top.n == 1 {
		plural = ""
	}
	e := Entry{Kind: "pull-secret", Value: top.name, Source: "pod specs", Completeness: Complete, Namespaces: []string{ns},
		Evidence: fmt.Sprintf("named in the pod specs of %d running workload%s that pulled from %s; Pacto did not read the Secret", top.n, plural, host)}
	if reg.Hint != "" {
		e.Hint = fitting("--set registryPullSecret=" + top.name)
	}
	if !clusterWide {
		return partial(e, watchedOnly)
	}
	return e
}

// imageHost is the registry host of image, as [Registries] counts it.
func imageHost(image string) string {
	c, _ := candidate(image)
	host, _, _ := strings.Cut(c, "/")
	return host
}
