// Package inferredcontract turns each external system a fleet snapshot knows
// into its inferred contract: a minimal pacto.yaml naming the identity and a
// pacto.inferred.yaml whose surface lists the hosts, ports and client majors
// the fleet sees, each with the sources that saw it. It names no consumer.
// Publish pushes the contracts whose surface changed, signed by the caller's
// installation key, under <registry>/pacto-inferred/<identity>.
package inferredcontract

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Version is the discovery version an inferred contract's record names.
const Version = "1"

// Partial reports whether snap may miss an external's values: the snapshot is
// partial, or a target cannot say which external systems it uses.
func Partial(snap *fleet.FleetSnapshot) bool {
	return snap.Completeness == fleet.CompletenessPartial || fleet.NewQuery(snap).ExternalsUnreported()
}

// completeness is the surface completeness of snap: partial with the
// limitation codes that made it so, else complete.
func completeness(snap *fleet.FleetSnapshot) *contract.SurfaceCompleteness {
	if !Partial(snap) {
		return &contract.SurfaceCompleteness{State: contract.CoverageComplete}
	}
	var codes []string
	for _, l := range snap.Limitations {
		codes = append(codes, l.Code)
	}
	if fleet.NewQuery(snap).ExternalsUnreported() {
		codes = append(codes, fleet.LimitationExternalsUnreported)
	}
	slices.Sort(codes)
	return &contract.SurfaceCompleteness{State: contract.CoveragePartial, Limitations: slices.Compact(codes)}
}

// stamp is t as a surface time: RFC 3339 UTC in seconds.
func stamp(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// Surface is what snap shows of the external system key at now: the hosts
// and ports its inferred sources name and the client majors the inferred
// record of each repository source's revision lists, each with the fleet
// source ids that saw it. It names no consumer.
func Surface(snap *fleet.FleetSnapshot, key fleet.ExternalKey, now time.Time) contract.InferredSurface {
	s := contract.InferredSurface{Identity: string(key), Class: externalcatalog.ClassUnclassified, AsOf: stamp(now), Completeness: completeness(snap)}
	if rec := snap.Externals[key]; rec != nil {
		s.Class = rec.Class
		if m := rec.Catalog; m != nil {
			s.Catalog = &contract.InferredCatalogMatch{Origin: m.Origin, Digest: m.Digest, Entry: m.Entry}
		}
	}
	hosts, ports, clients := values{}, values{}, values{}
	for _, r := range snap.Relationships {
		if r.ToExternal != key {
			continue
		}
		for _, src := range r.InferredSources {
			if src.Host != "" {
				hosts.add(src.Host, src)
			}
			if _, port, ok := strings.Cut(src.Host, ":"); ok {
				ports.add(port, src)
			}
			if c := fleet.ExternalClientOf(snap, src.Revision, key); c != nil {
				clients.add(fmt.Sprintf("%s/%d", c.SDK, c.Major), src)
			}
		}
	}
	s.Hosts, s.Ports, s.Clients = hosts.list(), ports.list(), clients.list()
	return s
}

// values collects one surface list by value.
type values map[string]*contract.SurfaceValue

// add records that src saw v: its source id, the first catalog digest named
// and the window of the times it was read.
func (vs values) add(v string, src fleet.InferredSource) {
	sv := vs[v]
	if sv == nil {
		sv = &contract.SurfaceValue{Value: v}
		vs[v] = sv
	}
	if !slices.Contains(sv.Sources, src.Source) {
		sv.Sources = append(sv.Sources, src.Source)
	}
	if sv.Catalog == "" {
		sv.Catalog = src.Catalog
	}
	if src.ObservedAt == nil {
		return
	}
	t := stamp(*src.ObservedAt)
	if sv.FirstSeen == "" || t < sv.FirstSeen {
		sv.FirstSeen = t
	}
	if t > sv.LastSeen {
		sv.LastSeen = t
	}
}

// list is the collected values, sorted by value, each source list sorted.
func (vs values) list() []contract.SurfaceValue {
	out := make([]contract.SurfaceValue, 0, len(vs))
	for _, v := range vs {
		slices.Sort(v.Sources)
		out = append(out, *v)
	}
	return sortValues(out)
}

func sortValues(vals []contract.SurfaceValue) []contract.SurfaceValue {
	slices.SortFunc(vals, func(a, b contract.SurfaceValue) int { return strings.Compare(a.Value, b.Value) })
	if len(vals) == 0 {
		return nil
	}
	return vals
}

// Merge is cur plus every value of prev cur no longer lists, with prev's
// provenance: a partial snapshot adds values and never removes one.
func Merge(prev, cur contract.InferredSurface) contract.InferredSurface {
	cur.Hosts = mergeValues(prev.Hosts, cur.Hosts)
	cur.Ports = mergeValues(prev.Ports, cur.Ports)
	cur.Clients = mergeValues(prev.Clients, cur.Clients)
	return cur
}

func mergeValues(prev, cur []contract.SurfaceValue) []contract.SurfaceValue {
	out := slices.Clone(cur)
	for _, p := range prev {
		if !slices.ContainsFunc(cur, func(c contract.SurfaceValue) bool { return c.Value == p.Value }) {
			out = append(out, p)
		}
	}
	return sortValues(out)
}
