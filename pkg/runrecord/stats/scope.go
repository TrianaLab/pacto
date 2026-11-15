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
		names = append(names, s.Name)
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

// sees reports whether a restricted caller may see r.
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

// visibleEntries keeps the entries the caller may see and sums the skipped
// counts of the anchors it may see: those of the visible entries' services,
// of the names in its view and of service, the filter, that Place accepts.
// outside says whether anything was left out; it is never a count.
func visibleEntries(entries []Entry, skipped map[string]Skipped, service string, sc Scope) (visible []Entry, visibleSkipped Skipped, outside bool) {
	anchors := map[string]bool{}
	for _, e := range entries {
		r := e.Record
		switch {
		case sc.NoDelivery != "" && r.Kind == runrecord.KindOperation:
		case sc.Restricted && !sc.sees(r):
			outside = true
		default:
			visible = append(visible, e)
			anchors[runrecord.AnchorTag(r.Service)] = true
		}
	}
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
		visibleSkipped = visibleSkipped.Add(s)
	}
	return visible, visibleSkipped, outside
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
