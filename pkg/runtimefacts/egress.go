package runtimefacts

import (
	"cmp"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// PodRef is one target pod and the node it is scheduled on.
type PodRef struct {
	UID  string
	Node string
}

// Egress is one host a target's pods reached, named by the DNS answers the calling pod received, with the
// most recent successful connection to it.
type Egress struct {
	Host          string
	Port          int32
	LastSuccessAt time.Time
}

// Unnamed counts the fresh successful connections to peers outside the cluster that carry no name: to a
// public address, to a private address nothing in the cluster explains, and to an address whose answers
// named more than one registrable domain.
type Unnamed struct {
	Public, Private, Ambiguous int
}

// privateRanges are the private address ranges: RFC 1918, carrier-grade NAT and IPv6 unique local.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// privateAddress reports whether a is in a private range.
func privateAddress(a netip.Addr) bool {
	return slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return p.Contains(a) })
}

// ExternalEgress returns the hosts the pods reached with a successful connection at or after since, sorted
// by host and port, and counts the connections it cannot name. A pod is read only from the snapshot of its
// own node: one node's agent never speaks for a pod on another node. A peer internal reports (a Service,
// endpoint, pod or node address), a loopback and a link-local peer are no egress; an unnamed private peer
// counts only when private does not explain it (a pod CIDR address of a pod that no longer exists).
func ExternalEgress(byNode map[string]*Snapshot, pods []PodRef, internal, private func(netip.Addr) bool,
	since time.Time) ([]Egress, Unnamed) {
	latest := map[Egress]time.Time{}
	var unnamed Unnamed
	for _, p := range pods {
		s := byNode[p.Node]
		if s == nil {
			continue
		}
		i := slices.IndexFunc(s.Workloads, func(w Workload) bool { return w.ID == p.UID })
		if i < 0 {
			continue
		}
		for _, c := range s.Workloads[i].Connections {
			a, ok := egressPeer(c, internal, since)
			switch {
			case !ok:
			case c.Host != "":
				key := Egress{Host: c.Host, Port: c.Port}
				if c.LastSuccessAt.After(latest[key]) {
					latest[key] = c.LastSuccessAt
				}
			case c.Ambiguous:
				unnamed.Ambiguous++
			case !privateAddress(a):
				unnamed.Public++
			case !private(a):
				unnamed.Private++
			}
		}
	}
	out := make([]Egress, 0, len(latest))
	for e, at := range latest {
		e.LastSuccessAt = at
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Egress) int { return cmp.Or(strings.Compare(a.Host, b.Host), cmp.Compare(a.Port, b.Port)) })
	return out, unnamed
}

// egressPeer is the connection's peer when it is a fresh success to an address outside the cluster.
func egressPeer(c Connection, internal func(netip.Addr) bool, since time.Time) (netip.Addr, bool) {
	if c.LastSuccessAt.IsZero() || c.LastSuccessAt.Before(since) {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(c.Address)
	if err != nil {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || internal(a) {
		return netip.Addr{}, false
	}
	return a, true
}
