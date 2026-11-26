package detect

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// NotDetected is the value of a Backstage entry when no Backstage runs in a
// watched namespace.
const NotDetected = "not detected"

// backstagePort is the Backstage backend's default port.
const backstagePort = 7007

// backstageImage reports whether repo is a Backstage backend image: its last
// segment is backstage, or starts with backstage- and ends in -app or
// -backend. A short known list, never any substring.
func backstageImage(repo string) bool {
	last := path.Base(repo)
	return last == "backstage" ||
		strings.HasPrefix(last, "backstage-") && (strings.HasSuffix(last, "-app") || strings.HasSuffix(last, "-backend"))
}

// Backstage finds an in-cluster Backstage catalog: a Service on port 7007 in a
// namespace running a Backstage image. Several Services in one namespace: the
// first is read and the entry says so. Candidates in more than one namespace
// give no Endpoint, so nobody wins the pick by a namespace that sorts first.
// No Backstage image gives a [NotDetected] entry, partial: an external
// Backstage is invisible here, so the entry never reads as "no catalog".
func Backstage(ws []Workload, svcs []Service) Entry {
	found, nss := running(ws, backstageImage)
	if len(found) == 0 {
		return partial(Entry{Kind: "backstage", Value: NotDetected, Evidence: "no Backstage image runs in a watched namespace", Source: tracesSource, Completeness: Complete},
			"an external Backstage is invisible to the cluster: set dashboard.backstage.url to read its owners and templates")
	}
	e := Entry{Kind: "backstage", Value: "Backstage", Evidence: workloadsFigure(len(found)), Source: tracesSource, Completeness: Complete}
	var cands []Object
	for _, s := range sortedServices(slices.Clone(svcs)) {
		if slices.Contains(nss, s.Namespace) && slices.ContainsFunc(s.Ports, func(p ServicePort) bool { return p.Port == backstagePort }) {
			cands = append(cands, Object{Kind: "Service", Namespace: s.Namespace, Name: s.Name})
		}
	}
	switch n := len(cands); {
	case n == 0:
		place(&e, nss, nil)
		return partial(e, fmt.Sprintf("no Service on port %d in front of it", backstagePort))
	case namespacesOfObjects(cands) > 1:
		place(&e, nil, capped(cands, MaxNamed))
		return partial(e, fmt.Sprintf("%d Backstage candidates in several namespaces; set dashboard.backstage.url", n))
	case n == 2:
		e.Details = []string{"1 more candidate; Pacto reads the first"}
	case n > 2:
		e.Details = []string{fmt.Sprintf("%d more candidates; Pacto reads the first", n-1)}
	}
	e.Endpoint = fmt.Sprintf("http://%s.%s.svc:%d", cands[0].Name, cands[0].Namespace, backstagePort)
	place(&e, nil, capped(cands, MaxNamed))
	return e
}

// namespacesOfObjects is how many namespaces objs sit in.
func namespacesOfObjects(objs []Object) int {
	nss := map[string]bool{}
	for _, o := range objs {
		nss[o.Namespace] = true
	}
	return len(nss)
}
