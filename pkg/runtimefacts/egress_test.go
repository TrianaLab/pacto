package runtimefacts

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
)

// egressNode is a node snapshot holding one pod workload with the given connections.
func egressNode(uid string, conns ...Connection) *Snapshot {
	return &Snapshot{Workloads: []Workload{{Identity: Identity{Kind: KindPod, ID: uid}, Connections: conns}}}
}

func okConn(addr, host string, port int32, at time.Time) Connection {
	return Connection{Address: addr, Host: host, Port: port, Successes: 1, LastSuccessAt: at}
}

// internalSet is a fixed set of cluster addresses.
func internalSet(addrs ...string) func(netip.Addr) bool {
	set := map[netip.Addr]bool{}
	for _, a := range addrs {
		set[netip.MustParseAddr(a)] = true
	}
	return func(a netip.Addr) bool { return set[a] }
}

func TestExternalEgressSkipsInternalAndFailures(t *testing.T) {
	byNode := map[string]*Snapshot{"a": egressNode("p1",
		okConn("54.1.1.1", "api.stripe.com", 443, ago(time.Second)),
		okConn("10.96.0.5", "checkout.example.com", 80, ago(time.Second)), // a ClusterIP
		Connection{Address: "54.2.2.2", Host: "down.example.com", Port: 443, Failures: 3, LastFailureAt: ago(time.Second)},
		okConn("127.0.0.1", "", 9000, ago(time.Second)),
		okConn("169.254.169.254", "", 80, ago(time.Second)),
		okConn("not-an-ip", "", 80, ago(time.Second)),
		okConn("::ffff:54.1.1.1", "api.stripe.com", 443, ago(2*time.Second)),
	)}
	named, unnamed := ExternalEgress(byNode, []PodRef{{UID: "p1", Node: "a"}}, internalSet("10.96.0.5"), internalSet(), ago(time.Minute))
	want := []Egress{{Host: "api.stripe.com", Port: 443, LastSuccessAt: ago(time.Second)}}
	if !reflect.DeepEqual(named, want) || unnamed != (Unnamed{}) {
		t.Fatalf("named = %+v, unnamed = %+v", named, unnamed)
	}
}

func TestExternalEgressFreshOnly(t *testing.T) {
	byNode := map[string]*Snapshot{"a": egressNode("p1",
		okConn("54.1.1.1", "api.stripe.com", 443, ago(2*time.Minute)),
		okConn("54.3.3.3", "", 443, ago(2*time.Minute)),
	)}
	named, unnamed := ExternalEgress(byNode, []PodRef{{UID: "p1", Node: "a"}}, internalSet(), internalSet(), ago(time.Minute))
	if len(named) != 0 || unnamed != (Unnamed{}) {
		t.Fatalf("history counts: named = %+v, unnamed = %+v", named, unnamed)
	}
}

func TestExternalEgressCountsUnnamed(t *testing.T) {
	byNode := map[string]*Snapshot{"a": egressNode("p1",
		okConn("54.1.1.1", "", 443, ago(time.Second)),    // public
		okConn("10.20.0.9", "", 5432, ago(time.Second)),  // private, unexplained
		okConn("10.244.1.7", "", 8080, ago(time.Second)), // private, inside a pod CIDR
		okConn("fd00::1", "", 443, ago(time.Second)),     // ULA, unexplained
		okConn("100.64.0.1", "", 443, ago(time.Second)),  // CGNAT, unexplained
		Connection{Address: "104.16.0.1", Port: 443, Ambiguous: true, Successes: 1, LastSuccessAt: ago(time.Second)},
	)}
	named, unnamed := ExternalEgress(byNode, []PodRef{{UID: "p1", Node: "a"}}, internalSet(), internalSet("10.244.1.7"), ago(time.Minute))
	if len(named) != 0 {
		t.Fatalf("named = %+v", named)
	}
	if want := (Unnamed{Public: 1, Private: 3, Ambiguous: 1}); unnamed != want {
		t.Fatalf("unnamed = %+v, want %+v", unnamed, want)
	}
}

func TestExternalEgressIgnoresOtherNodesClaim(t *testing.T) {
	// Node b's snapshot names a pod scheduled on node a: one node's agent never speaks for another's pod.
	byNode := map[string]*Snapshot{
		"a": egressNode("other"),
		"b": egressNode("p1", okConn("54.1.1.1", "api.stripe.com", 443, ago(time.Second))),
	}
	named, unnamed := ExternalEgress(byNode, []PodRef{{UID: "p1", Node: "a"}, {UID: "p2", Node: "c"}}, internalSet(), internalSet(), ago(time.Minute))
	if len(named) != 0 || unnamed != (Unnamed{}) {
		t.Fatalf("a foreign claim counted: named = %+v, unnamed = %+v", named, unnamed)
	}
}

func TestExternalEgressSortsAndMerges(t *testing.T) {
	byNode := map[string]*Snapshot{
		"a": egressNode("p1", okConn("54.1.1.1", "api.stripe.com", 443, ago(3*time.Second)), okConn("52.1.1.1", "a.s3.amazonaws.com", 443, ago(time.Second))),
		"b": egressNode("p2", okConn("54.1.1.2", "api.stripe.com", 443, ago(time.Second))),
	}
	named, _ := ExternalEgress(byNode, []PodRef{{UID: "p1", Node: "a"}, {UID: "p2", Node: "b"}}, internalSet(), internalSet(), ago(time.Minute))
	want := []Egress{
		{Host: "a.s3.amazonaws.com", Port: 443, LastSuccessAt: ago(time.Second)},
		{Host: "api.stripe.com", Port: 443, LastSuccessAt: ago(time.Second)},
	}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("named = %+v\nwant %+v", named, want)
	}
}

func TestPrivateAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.1.2.3": true, "172.16.0.1": true, "192.168.1.1": true, "100.64.0.1": true, "fd12::1": true,
		"54.1.1.1": false, "100.128.0.1": false, "2600::1": false,
	} {
		if got := privateAddress(netip.MustParseAddr(addr)); got != want {
			t.Errorf("privateAddress(%s) = %v, want %v", addr, got, want)
		}
	}
}
