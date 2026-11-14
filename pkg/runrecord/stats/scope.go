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
}

// FleetPlacement places a service with full, the unrestricted snapshot. A
// name the snapshot does not know or cannot tell apart is unplaced. With a
// view, the resolved service must also be in it, so a bare name never lands
// on a visible service when the snapshot holds a hidden one of that name.
func FleetPlacement(full, view *fleet.Query) func(string) (Placement, bool) {
	return memoize(func(name string) (Placement, bool) {
		sv, err := full.GetService(name)
		if err != nil {
			return Placement{}, false
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

// sees reports whether a restricted caller may see r.
func (sc Scope) sees(r runrecord.Record) bool {
	if _, ok := sc.place(r.Service); !ok {
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
// counts of the anchors it may see: those of the visible entries' services
// and of service, the filter, when the caller can place it. outside says
// whether anything was left out; it is never a count.
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
	if _, ok := sc.place(service); ok && service != "" {
		anchors[runrecord.AnchorTag(service)] = true
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
		p, ok := sc.place(r.Service)
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
		if strings.HasPrefix(g.Key, "(") {
			return 1
		}
		return 0
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(unnamed(a)-unnamed(b), fleet.NaturalCompare(a.Label, b.Label), strings.Compare(a.Key, b.Key))
	})
	return out
}
