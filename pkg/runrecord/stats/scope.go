package stats

import (
	"cmp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// Limitation codes of a narrowed answer.
const (
	LimitationOutsideView         = "OUTSIDE_YOUR_VIEW"
	LimitationDeliveryUnavailable = "DELIVERY_UNAVAILABLE_OVER_HTTP"
	LimitationAttributionPartial  = "ATTRIBUTION_PARTIAL"
	LimitationCoverageOutsideView = "COVERAGE_OUTSIDE_YOUR_VIEW"
	LimitationOtherClusters       = "OTHER_CLUSTERS_NOT_SHOWN"
	LimitationHomeClusterUnknown  = "HOME_CLUSTER_UNKNOWN"
)

// The keys of the groups no record value names. The parentheses keep them
// apart from a real value spelled the same, such as a domain named unknown.
const (
	keyUnknown = "(unknown)"
	keyUnowned = "(unowned)"
	keyNone    = "(none)"
)

// Placement is where the fleet snapshot puts a service.
type Placement struct {
	OwnerKey      string
	OwnerLabel    string
	Domain        string
	DomainUnknown bool
}

// OperationAccess is what a restricted caller's operation review found: the
// Stage namespaces they may list operations in, and the ids of the clusters
// those namespaces were reviewed in ([Scope.Clusters]).
type OperationAccess struct {
	Visible  func(namespace string) bool
	Clusters []string
}

// Scope narrows what a caller may see and says where services are placed.
type Scope struct {
	// Restricted leaves out every record whose service Place cannot place
	// and every operation record OperationVisible refuses. An unrestricted
	// reader sees every record and groups an unplaced service as unknown.
	Restricted bool
	// Place resolves a record's service to its owner and domain.
	Place func(service string) (Placement, bool)
	// OperationVisible says whether the caller may list operations in a
	// Stage namespace.
	OperationVisible func(namespace string) bool
	// Clusters are the ids of the clusters a restricted caller's operation
	// access was reviewed in. An operation record whose Stage names another
	// cluster is hidden before its namespace is looked at; nil checks no
	// cluster, and an empty list hides every record that names one.
	Clusters []string
	// NoDelivery, when set, drops every operation record and is the message
	// of the limitation that says so.
	NoDelivery string
	// Snapshot is the fleet snapshot owner and domain are resolved from.
	Snapshot *Attribution
	// Names are the service names in a restricted caller's view. A
	// restricted read reads only their anchors, and a skipped record counts
	// under the anchor of a name Place accepts.
	Names []string
}

// VisibleNames is the sorted, unique service names view holds.
func VisibleNames(view *fleet.Query) []string {
	var names []string
	// ponytail: read-only walk of the shared snapshot; bounded by its size.
	for _, s := range view.SnapshotForSerialization().Services {
		// An inferred service has no registry anchor to read.
		if !s.Inferred() {
			names = append(names, s.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// FleetPlacement places a service, a bare name or a service key, with full,
// the unrestricted snapshot. A name the snapshot does not know is unplaced,
// and so is a bare name more than one service carries, even when one of them
// is the default-domain service whose key is that name: run records name
// services by their bare name only. With a view, the resolved service must
// also be in it, so a bare name never lands on a visible service when the
// snapshot holds a hidden one of that name.
func FleetPlacement(full, view *fleet.Query) func(string) (Placement, bool) {
	var named map[string]int
	return memoize(func(name string) (Placement, bool) {
		sv, err := full.GetService(name)
		if err != nil {
			return Placement{}, false
		}
		if domain, _ := fleet.ParseServiceKey(fleet.ServiceKey(name)); domain == "" {
			if named == nil {
				named = map[string]int{}
				for _, s := range full.SnapshotForSerialization().Services {
					named[s.Name]++
				}
			}
			if named[sv.Service.Name] > 1 {
				return Placement{}, false
			}
		}
		if view != nil {
			if _, err := view.GetService(string(sv.Service.Key)); err != nil {
				return Placement{}, false
			}
		}
		s := sv.Service
		p := Placement{OwnerKey: s.Owner.KeyString(), Domain: s.BusinessDomain, DomainUnknown: s.BusinessDomainUnknown}
		if p.OwnerKey != "" {
			p.OwnerLabel = fleet.OwnerRef(p.OwnerKey).Label
		}
		return p, true
	})
}

// memoize resolves each name once.
// ponytail: one resolve per distinct service name; bounded by MaxRecords.
// Not safe for concurrent use; Aggregate calls it from one goroutine.
func memoize(resolve func(string) (Placement, bool)) func(string) (Placement, bool) {
	type result struct {
		p  Placement
		ok bool
	}
	seen := map[string]result{}
	return func(name string) (Placement, bool) {
		r, ok := seen[name]
		if !ok {
			r.p, r.ok = resolve(name)
			seen[name] = r
		}
		return r.p, r.ok
	}
}

func (sc Scope) place(service string) (Placement, bool) {
	if sc.Place == nil {
		return Placement{}, false
	}
	return sc.Place(service)
}

// recordService is the service r names for placement: an operation record
// that carries its service's domain names the service key, else the bare name.
func recordService(r runrecord.Record) string {
	if r.Kind == runrecord.KindOperation && r.ServiceDomain != "" {
		return string(fleet.NewServiceKeyDomain(r.ServiceDomain, r.Service))
	}
	return r.Service
}

// sees reports whether a restricted caller may see r, a record that names a
// service. visibleEntries keeps every coverage record from a restricted
// caller first: it is fleet-wide and would tell them the size of a fleet they
// may only partly read.
func (sc Scope) sees(r runrecord.Record) bool {
	if _, ok := sc.place(recordService(r)); !ok {
		return false
	}
	if r.Kind != runrecord.KindOperation {
		return true
	}
	ns := ""
	if r.Stage != nil {
		ns = r.Stage.Namespace
	}
	return sc.OperationVisible != nil && sc.OperationVisible(ns)
}

// homeUnknown reports a restricted caller whose sign-in cluster's identity
// could not be read: every record that names a cluster is hidden, and none is
// known to come from another cluster.
func (sc Scope) homeUnknown() bool { return sc.Clusters != nil && len(sc.Clusters) == 0 }

// otherCluster reports whether r is an operation record whose Stage names a
// cluster the caller's access was not reviewed in.
func (sc Scope) otherCluster(r runrecord.Record) bool {
	return sc.Clusters != nil && r.Kind == runrecord.KindOperation && r.Stage != nil &&
		r.Stage.Cluster != "" && !slices.Contains(sc.Clusters, r.Stage.Cluster)
}

// visibleEntries keeps the entries the caller may see and sums the skipped
// counts of the anchors it may see: those of the visible entries' services,
// of the names in its view and of service, the filter, that Place accepts.
// outside says whether a record naming a service was left out, coverage
// whether a fleet-wide coverage record was; neither is ever a count.
// other holds only the other-cluster operation records of services the
// caller can see, for the caller to count those the query asks about; one of
// a service it cannot place only sets outside.
func visibleEntries(entries []Entry, skipped map[string]Skipped, service string, sc Scope) (visible []Entry, visibleSkipped Skipped, outside, coverage bool, other []Entry) {
	anchors := map[string]bool{}
	for _, e := range entries {
		r := e.Record
		switch {
		case sc.NoDelivery != "" && r.Kind == runrecord.KindOperation:
		case sc.Restricted && r.Kind == runrecord.KindCoverage:
			coverage = true
		case sc.Restricted && sc.otherCluster(r):
			if _, ok := sc.place(recordService(r)); ok {
				other = append(other, e)
			} else {
				outside = true
			}
		case sc.Restricted && !sc.sees(r):
			outside = true
		default:
			visible = append(visible, e)
			anchors[runrecord.AnchorTag(r.Service)] = true
		}
	}
	visibleSkipped, skippedOutside := skippedIn(anchors, skipped, service, sc)
	return visible, visibleSkipped, outside || skippedOutside, coverage, other
}

// skippedIn sums the skipped counts of anchors, the visible entries'
// anchors, plus those of service and the names in sc's view that Place
// accepts; outside says whether a restricted caller was kept from any count.
func skippedIn(anchors map[string]bool, skipped map[string]Skipped, service string, sc Scope) (visible Skipped, outside bool) {
	for _, name := range append([]string{service}, sc.Names...) {
		if _, ok := sc.place(name); ok && name != "" {
			anchors[runrecord.AnchorTag(name)] = true
		}
	}
	for anchor, s := range skipped {
		if sc.Restricted && !anchors[anchor] {
			outside = outside || s.Total() > 0
			continue
		}
		visible = visible.Add(s)
	}
	return visible, outside
}

// truncatedService names the service whose listing was cut, when a visible
// entry was listed under that anchor; otherwise nothing.
func truncatedService(anchor string, visible []Entry) string {
	for _, e := range visible {
		if anchor != "" && runrecord.AnchorTag(e.Record.Service) == anchor {
			return e.Record.Service
		}
	}
	return ""
}

// groupKey is the group a record counts in.
func groupKey(r runrecord.Record, by string, sc Scope) (key, label string) {
	switch by {
	case "owner", "domain":
		p, ok := sc.place(recordService(r))
		switch {
		case !ok || by == "domain" && p.DomainUnknown:
			return keyUnknown, "unknown"
		case by == "domain" && p.Domain == "":
			return keyNone, "none"
		case by == "domain":
			return p.Domain, p.Domain
		case p.OwnerKey == "":
			return keyUnowned, "unowned"
		}
		return p.OwnerKey, p.OwnerLabel
	case "repository":
		if r.Repository == "" {
			return keyUnknown, "unknown"
		}
		return r.Repository, r.Repository
	case "environment":
		if env, _, ok := environmentOf(r); ok {
			return env, env
		}
		return keyNone, "none"
	}
	return r.Service, r.Service
}

// groups runs the metrics per group, alphabetical, with the groups no value
// names last.
func groups(entries, publishes []Entry, by string, sc Scope) []Group {
	if by == "" {
		return nil
	}
	labels, members := map[string]string{}, map[string][]Entry{}
	for _, e := range entries {
		k, l := groupKey(e.Record, by, sc)
		labels[k], members[k] = l, append(members[k], e)
	}
	out := make([]Group, 0, len(members))
	for k, es := range members {
		m, _ := metricsOf(es, publishes)
		out = append(out, Group{Key: k, Label: labels[k], Metrics: m})
	}
	// Every comparison runs whatever order the map yields, so coverage
	// never depends on it.
	unnamed := func(g Group) int {
		if g.Key == keyUnknown || g.Key == keyUnowned || g.Key == keyNone {
			return 1
		}
		return 0
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(unnamed(a)-unnamed(b), fleet.NaturalCompare(a.Label, b.Label), strings.Compare(a.Key, b.Key))
	})
	return out
}

// OperationNamespaces is the sorted, unique Stage namespaces the operation
// records of set name: the namespaces a caller's access is reviewed in.
func OperationNamespaces(set RecordSet) []string {
	var out []string
	for _, e := range set.Entries {
		if e.Record.Kind == runrecord.KindOperation && e.Record.Stage != nil && e.Record.Stage.Namespace != "" {
			out = append(out, e.Record.Stage.Namespace)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
