package detect

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The reads the operator bounds: more running pods or nodes than these make
// the entries built from them partial.
const (
	MaxPods  = 20000
	MaxNodes = 5000
)

// Reasons shared by the rules.
const (
	noMajority  = "no clear majority"
	watchedOnly = "read only the watched namespaces"
	// officialImages is Docker Hub's official images: nobody pushes there, so
	// it is never proposed as a push target.
	officialImages = "docker.io/library"
)

// Workload is one running workload: a controller owner (or a bare pod), its
// namespace, the images its pods run and the imagePullSecrets their pod specs
// name (ServiceAccount pull Secrets are merged into the pod spec at
// admission). No Secret is read.
type Workload struct {
	ID, Namespace string
	Images        []string
	PullSecrets   []string
}

// Registries is the registry most namespaces pull from. Each candidate is an
// image's registry plus its first path segment, ranked by the number of
// distinct namespaces whose workloads use it (one vote per namespace, so one
// tenant creating many workloads cannot win), then by workload share. The
// hint appears only on a clear majority. sampled says the pod read stopped at
// MaxPods; clusterWide says every namespace was read.
func Registries(ws []Workload, sampled, clusterWide bool) Entry {
	t := tallies{}
	counted, namespaces := 0, map[string]bool{}
	for _, w := range ws {
		seen := map[string]bool{}
		for _, img := range w.Images {
			if c, ok := candidate(img); ok && !seen[c] {
				seen[c] = true
				t.add(c, w.Namespace)
			}
		}
		if len(seen) > 0 {
			counted++
			namespaces[w.Namespace] = true
		}
	}
	if counted == 0 {
		return UnknownEntry("registry", "no running pod with a readable image was found")
	}
	ranked := t.ranked()
	figure := func(c *tally) string {
		return fmt.Sprintf("%d of %d namespaces, %d%% of workloads", len(c.ns), len(namespaces), int(math.Round(float64(c.n)*100/float64(counted))))
	}
	e := Entry{Kind: "registry", Value: ranked[0].name, Evidence: "in " + figure(ranked[0]), Source: "running pods", Completeness: Complete}
	for _, c := range capped(ranked, MaxDetails) {
		e.Details = append(e.Details, c.name+": "+figure(c))
	}
	var reasons []string
	if sampled {
		reasons = append(reasons, fmt.Sprintf("sampled the first %d running pods", MaxPods))
	}
	switch {
	case !majority(ranked, len(namespaces)):
		reasons = append(reasons, noMajority)
	case ranked[0].name != officialImages:
		e.Hint = fitting("oci://" + ranked[0].name + "/<service>:<version>")
	}
	if !clusterWide {
		reasons = append(reasons, watchedOnly)
		place(&e, slices.Sorted(maps.Keys(namespaces)), nil)
	} else {
		e.Scope = fleet.ScopeCluster
	}
	return partial(e, reasons...)
}

// candidate is the registry plus first path segment of image: ghcr.io/acme
// for ghcr.io/acme/app, docker.io/library for nginx. A one-segment repository
// gives the registry alone.
func candidate(image string) (string, bool) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", false
	}
	reg := ref.Context().RegistryStr()
	if reg == name.DefaultRegistry {
		reg = "docker.io"
	}
	if first, _, ok := strings.Cut(ref.Context().RepositoryStr(), "/"); ok {
		return reg + "/" + first, true
	}
	return reg, true
}

// tally is one candidate: the distinct namespaces voting for it and how many
// things (workloads, routes, nodes) use it.
type tally struct {
	name string
	ns   map[string]bool
	n    int
}

type tallies map[string]*tally

// add counts one use of name from namespace ns ("" counts no namespace).
func (t tallies) add(name, ns string) {
	c, ok := t[name]
	if !ok {
		c = &tally{name: name, ns: map[string]bool{}}
		t[name] = c
	}
	c.n++
	if ns != "" {
		c.ns[ns] = true
	}
}

// ranked is every candidate by distinct namespaces, then uses, then name.
func (t tallies) ranked() []*tally {
	return slices.SortedFunc(maps.Values(t), func(a, b *tally) int {
		return cmp.Or(cmp.Compare(len(b.ns), len(a.ns)), cmp.Compare(b.n, a.n), strings.Compare(a.name, b.name))
	})
}

// majority says the top of ranked spans at least two namespaces and a strict
// majority of the total counted, and leads the runner-up on namespaces: votes
// are not exclusive (a pod running an app and a sidecar votes twice), so a tie
// is no clear majority.
func majority(ranked []*tally, total int) bool {
	top := ranked[0]
	return len(top.ns) >= 2 && 2*len(top.ns) > total && (len(ranked) == 1 || len(top.ns) > len(ranked[1].ns))
}

// fitting is hint, or nothing when it is longer than a reader keeps: a cut
// command would be wrong.
// ponytail: the rare over-long name loses its hint silently; the row still names the value.
func fitting(hint string) string {
	if utf8.RuneCountInString(hint) > MaxRunes {
		return ""
	}
	return hint
}

// place keeps the namespaces and objects an entry's facts come from, or
// marks it cluster-scoped when there are more than a reader keeps (each
// becomes one row object, capped at fleet.MaxProviderObjects), so a
// restricted view never sees an entry whose cut list hides a namespace.
func place(e *Entry, namespaces []string, objects []Object) {
	if len(namespaces)+len(objects) > fleet.MaxProviderObjects {
		e.Scope = fleet.ScopeCluster
		return
	}
	e.Namespaces, e.Objects = namespaces, objects
}

// within places e's facts: on a cluster-wide read (watched nil) the entry is
// cluster-scoped; on a namespace-list read it names namespaces and objects,
// or every watched namespace when it found nothing to name, so "none found"
// never speaks for a namespace a reader may not read.
func within(e *Entry, watched, namespaces []string, objects []Object) {
	if watched == nil {
		e.Scope = fleet.ScopeCluster
		return
	}
	if len(namespaces) == 0 && len(objects) == 0 {
		namespaces = watched
	}
	place(e, namespaces, objects)
}

// partial is e, partial with reasons when there are any.
func partial(e Entry, reasons ...string) Entry {
	if len(reasons) > 0 {
		e.Completeness = Partial
		e.Reason = strings.Join(reasons, "; ")
	}
	return e
}
