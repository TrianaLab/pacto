/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

var (
	stripeAddr = netip.MustParseAddr("203.0.113.7")
	stripe6    = netip.MustParseAddr("2001:db8::7")
)

// answerMsg builds a DNS response to name with one record per answer: an address becomes A or AAAA,
// anything else a CNAME to that name.
func answerMsg(t *testing.T, hdr dnsmessage.Header, name string, answers ...any) []byte {
	t.Helper()
	hdr.Response = true
	b := dnsmessage.NewBuilder(nil, hdr)
	b.EnableCompression()
	q := dnsmessage.MustNewName(name + ".")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.StartQuestions())
	must(b.Question(dnsmessage.Question{Name: q, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}))
	must(b.StartAnswers())
	rh := dnsmessage.ResourceHeader{Name: q, Class: dnsmessage.ClassINET, TTL: 60}
	for _, a := range answers {
		switch v := a.(type) {
		case netip.Addr:
			if v.Is4() {
				must(b.AResource(rh, dnsmessage.AResource{A: v.As4()}))
			} else {
				must(b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: v.As16()}))
			}
		case string:
			must(b.CNAMEResource(rh, dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(v + ".")}))
			rh.Name = dnsmessage.MustNewName(v + ".")
		}
	}
	msg, err := b.Finish()
	must(err)
	return msg
}

func answer(t *testing.T, name string, addrs ...any) []byte {
	return answerMsg(t, dnsmessage.Header{}, name, addrs...)
}

func TestParseDNSAnswerQuestionNameAndAddresses(t *testing.T) {
	name, addrs, ok := parseDNSAnswer(answer(t, "API.Stripe.com", stripeAddr, stripe6))
	if !ok || name != "api.stripe.com" || !slices.Equal(addrs, []netip.Addr{stripeAddr, stripe6}) {
		t.Fatalf("got %q %v %v", name, addrs, ok)
	}
	if _, _, ok := parseDNSAnswer(answer(t, "api.stripe.com")); ok {
		t.Fatal("an answer with no address names nothing")
	}
}

func TestParseDNSAnswerCNAMEChainKeepsQuestion(t *testing.T) {
	name, addrs, ok := parseDNSAnswer(answer(t, "api.stripe.com", "edge.cdn.example.net", stripeAddr))
	if !ok || name != "api.stripe.com" || !slices.Equal(addrs, []netip.Addr{stripeAddr}) {
		t.Fatalf("got %q %v %v", name, addrs, ok)
	}
}

func TestParseDNSAnswerRejectsQueryAndTruncated(t *testing.T) {
	query := answer(t, "api.stripe.com", stripeAddr)
	query[2] &^= 0x80 // clear QR
	for name, msg := range map[string][]byte{
		"query":     query,
		"truncated": answerMsg(t, dnsmessage.Header{Truncated: true}, "api.stripe.com", stripeAddr),
	} {
		if _, _, ok := parseDNSAnswer(msg); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestParseDNSAnswerRejectsErrorRcode(t *testing.T) {
	if _, _, ok := parseDNSAnswer(answerMsg(t, dnsmessage.Header{RCode: dnsmessage.RCodeNameError}, "api.stripe.com", stripeAddr)); ok {
		t.Fatal("NXDOMAIN accepted")
	}
}

func TestParseDNSAnswerSkipsClusterNames(t *testing.T) {
	for _, n := range []string{"payments.shop.svc.cluster.local", "10-1-2-3.shop.pod.cluster.local"} {
		if _, _, ok := parseDNSAnswer(answer(t, n, stripeAddr)); ok {
			t.Errorf("%s accepted", n)
		}
	}
}

// TestParseDNSAnswerMalformed cuts a valid answer at every length and corrupts an answer record: none
// panics, and a cut message never yields an address it did not hold.
func TestParseDNSAnswerMalformed(t *testing.T) {
	msg := answer(t, "api.stripe.com", stripeAddr, stripe6)
	for i := range len(msg) {
		if _, addrs, ok := parseDNSAnswer(msg[:i]); ok && len(addrs) > 2 {
			t.Fatalf("cut at %d: %v", i, addrs)
		}
	}
	if _, _, ok := parseDNSAnswer(nil); ok {
		t.Fatal("empty payload accepted")
	}
	// A header that promises a question it does not carry.
	if _, _, ok := parseDNSAnswer([]byte{0, 0, 0x80, 0, 0, 1, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("missing question accepted")
	}
	// A second question that cannot be read.
	two := answer(t, "api.stripe.com", stripeAddr)
	two[5] = 2
	if _, _, ok := parseDNSAnswer(two); ok {
		t.Fatal("unreadable second question accepted")
	}
	// An A record whose data is cut short stops the answer section; an AAAA likewise.
	for _, addr := range []netip.Addr{stripeAddr, stripe6} {
		m := answer(t, "api.stripe.com", addr)
		m[len(m)-addr.BitLen()/8-1]-- // shrink RDLENGTH by one
		if _, _, ok := parseDNSAnswer(m[:len(m)-1]); ok {
			t.Errorf("%v: short record accepted", addr)
		}
	}
	// A CNAME whose target cannot be read.
	cn := answer(t, "api.stripe.com", "edge.example.net")
	if _, _, ok := parseDNSAnswer(cn[:len(cn)-2]); ok {
		t.Fatal("cut CNAME accepted")
	}
}

func TestParseDNSAnswerRejectsNonLDHName(t *testing.T) {
	for _, n := range []string{`a"b.example.com`, "a<b.example.com", "caf\xe9.example.com", "a_b.example.com"} {
		if _, ok := ldhHost(n); ok {
			t.Errorf("%q accepted", n)
		}
	}
	long := strings.Repeat(strings.Repeat("a", 62)+".", 4) + "co" // 254 bytes
	for _, n := range []string{"", ".", long, strings.Repeat("a", 64) + ".com", "a..com"} {
		if _, ok := ldhHost(n); ok {
			t.Errorf("%q accepted", n)
		}
	}
	// The parser path: a question with a quote is escaped by dnsmessage and refused.
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("a\"b.example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	msg, _ := b.Finish()
	if _, _, ok := parseDNSAnswer(msg); ok {
		t.Fatal("quoted question accepted")
	}
	if h, ok := ldhHost("API.Example.COM."); !ok || h != "api.example.com" {
		t.Fatalf("ldhHost = %q %v", h, ok)
	}
}

func dnsEvent(ns uint32, msg []byte) Event {
	return Event{Kind: EventDNSAnswer, Netns: ns, DNS: msg}
}

func dnsRunning() CollectorStatus { return CollectorStatus{DNSState: runtimefacts.StateRunning} }

// connectionTo is the connection of a published workload to addr.
func connectionTo(t *testing.T, ws []runtimefacts.Workload, uid string, addr netip.Addr) runtimefacts.Connection {
	t.Helper()
	for _, c := range podByUID(t, ws, uid).Connections {
		if c.Address == addr.String() {
			return c
		}
	}
	t.Fatalf("pod %s has no connection to %v", uid, addr)
	return runtimefacts.Connection{}
}

func TestAggregatorNamesConnectionFromWorkloadAnswer(t *testing.T) {
	a := NewAggregator(DefaultBounds(), byCgroup(uidA))
	a.Resync(oneScan(), t0)
	a.Apply(inCgroup(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), 1), at(time.Second))
	a.Apply(dnsEvent(100, []byte{1, 2}), at(time.Second)) // unparseable: ignored
	a.Apply(connect(100, stripeAddr, 443, true), at(2*time.Second))
	_, ws, dns := a.Snapshot(at(3*time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "api.stripe.com" || c.Ambiguous {
		t.Fatalf("connection = %+v", c)
	}
	if dns != (runtimefacts.DNSCollection{State: runtimefacts.StateRunning}) {
		t.Fatalf("dns = %+v", dns)
	}
}

// TestAggregatorNeverNamesFromAnotherWorkload: pod C's answer never names pod A's call to the same
// address, and a shared workload's answers are dropped.
func TestAggregatorNeverNamesFromAnotherWorkload(t *testing.T) {
	a := NewAggregator(DefaultBounds(), byCgroup(uidA, uidB, uidC))
	scan := oneScan()
	scan.Netns[300] = &NetnsScan{Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}}
	a.Resync(scan, t0)
	a.Apply(inCgroup(dnsEvent(300, answer(t, "api.stripe.com", stripeAddr)), 3), at(time.Second))
	a.Apply(inCgroup(dnsEvent(1, answer(t, "api.stripe.com", stripeAddr)), 2), at(time.Second)) // pod B: shared
	a.Apply(dnsEvent(1, answer(t, "api.stripe.com", stripeAddr)), at(time.Second))              // host netns: shared
	a.Apply(connect(100, stripeAddr, 443, true), at(time.Second))
	a.Apply(connect(300, stripeAddr, 443, true), at(time.Second))
	_, ws, dns := a.Snapshot(at(2*time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "" {
		t.Fatalf("pod A named from pod C's answer: %+v", c)
	}
	if c := connectionTo(t, ws, uidC, stripeAddr); c.Host != "api.stripe.com" {
		t.Fatalf("pod C = %+v", c)
	}
	if dns.State != runtimefacts.StateRunning {
		t.Fatalf("a shared namespace's answer degraded DNS: %+v", dns)
	}
}

// TestAggregatorDNSAnswerByNetns: an answer whose cgroup names nothing is attributed by namespace, and
// one from a namespace no scan has mapped is held and adopted with the connection.
func TestAggregatorDNSAnswerByNetns(t *testing.T) {
	a := NewAggregator(DefaultBounds(), byCgroup(uidA))
	a.Resync(oneScan(), t0)
	a.Apply(inCgroup(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), 9), at(time.Second))
	a.Apply(connect(100, stripeAddr, 443, true), at(time.Second))

	ev := dnsEvent(300, answer(t, "hooks.slack.com", apiAddr))
	ev.NetnsCookie = 30
	a.Apply(ev, at(time.Second))
	a.Apply(connect(300, apiAddr, 443, true), at(time.Second))
	// Pod C by cgroup before any scan saw it: held under its identity.
	b := NewAggregator(DefaultBounds(), byCgroup(uidA, uidB, uidC))
	b.Apply(inCgroup(dnsEvent(400, answer(t, "api.stripe.com", stripeAddr)), 3), t0)
	if b.pendingIDs[pod(uidC)] == nil || a.cookies[300] != 30 {
		t.Fatalf("pendingIDs = %v, cookies = %v", b.pendingIDs, a.cookies)
	}

	scan := oneScan()
	scan.Netns[300] = &NetnsScan{Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}}
	a.Resync(scan, at(2*time.Second))
	_, ws, _ := a.Snapshot(at(3*time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "api.stripe.com" {
		t.Fatalf("pod A = %+v", c)
	}
	if c := connectionTo(t, ws, uidC, apiAddr); c.Host != "hooks.slack.com" {
		t.Fatalf("pod C = %+v", c)
	}
}

func TestAggregatorUnattributedDNSDegraded(t *testing.T) {
	b := DefaultBounds()
	b.MaxWorkloads = 1
	a := NewAggregator(b, byCgroup(uidA, uidB))
	a.Apply(dnsEvent(500, answer(t, "api.stripe.com", stripeAddr)), t0) // fills the one pending set
	a.Apply(dnsEvent(501, answer(t, "api.stripe.com", stripeAddr)), t0) // nowhere to go
	a.Apply(inCgroup(dnsEvent(502, answer(t, "api.stripe.com", stripeAddr)), 2), t0)
	if _, _, dns := a.Snapshot(at(time.Second), dnsRunning()); dns.State != runtimefacts.StateDegraded || dns.Reason != ReasonDNSUnattributed {
		t.Fatalf("dns = %+v", dns)
	}
	if _, _, dns := a.Snapshot(at(11*time.Minute), dnsRunning()); dns.State != runtimefacts.StateRunning {
		t.Fatalf("after retention dns = %+v", dns)
	}
}

func TestAggregatorSharedAddressAcrossDomainsIsUnnamed(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), t0)
	a.Apply(dnsEvent(100, answer(t, "cdn.example.org", stripeAddr)), t0)
	a.Apply(connect(100, stripeAddr, 443, true), t0)
	_, ws, _ := a.Snapshot(at(time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "" || !c.Ambiguous {
		t.Fatalf("connection = %+v", c)
	}
}

// TestAggregatorEvictionKeepsEveryDomainOfAnAddress: four more names of one domain never push out the only
// name of another domain inside the retention window, so the address still names no vendor.
func TestAggregatorEvictionKeepsEveryDomainOfAnAddress(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(100, answer(t, "api.a.com", stripeAddr)), t0)
	for i, n := range []string{"b1.b.com", "b2.b.com", "b3.b.com", "b4.b.com"} {
		a.Apply(dnsEvent(100, answer(t, n, stripeAddr)), at(time.Duration(1+i)*time.Second))
	}
	a.Apply(connect(100, stripeAddr, 443, true), at(5*time.Second))
	_, ws, _ := a.Snapshot(at(6*time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "" || !c.Ambiguous {
		t.Fatalf("connection = %+v, want ambiguous", c)
	}
	// Every domain distinct: the least recently seen goes and the address stays ambiguous.
	b := NewAggregator(DefaultBounds(), nil)
	b.Resync(oneScan(), t0)
	for i, n := range []string{"a.one.com", "a.two.com", "a.three.com", "a.four.com", "a.five.com"} {
		b.Apply(dnsEvent(100, answer(t, n, stripeAddr)), at(time.Duration(i)*time.Second))
	}
	if l := b.workloads[pod(uidA)].names[stripeAddr]; len(l) != maxNamesPerAddress || slices.ContainsFunc(l, func(n dnsName) bool { return n.name == "a.one.com" }) {
		t.Fatalf("names = %v", l)
	}
}

func TestAggregatorSameDomainNamesKeepHost(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(100, answer(t, "files.stripe.com", stripeAddr)), t0)
	a.Apply(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), at(time.Second))
	a.Apply(dnsEvent(100, answer(t, "files.stripe.com", stripeAddr)), t0) // older repeat keeps its later time
	a.Apply(connect(100, stripeAddr, 443, true), t0)
	_, ws, _ := a.Snapshot(at(2*time.Second), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "api.stripe.com" || c.Ambiguous {
		t.Fatalf("connection = %+v", c)
	}
	if d := registrableDomain("localhost"); d != "localhost" {
		t.Fatalf("a name with no registrable domain groups as itself, got %q", d)
	}
}

func TestAggregatorDNSNamesCappedPerWorkload(t *testing.T) {
	b := DefaultBounds()
	b.MaxDNSNamesPerWorkload = 2
	a := NewAggregator(b, nil)
	a.Resync(oneScan(), t0)
	addrs := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("203.0.113.2"), netip.MustParseAddr("203.0.113.3")}
	for i, addr := range addrs {
		a.Apply(dnsEvent(100, answer(t, "api.stripe.com", addr)), at(time.Duration(i)*time.Second))
	}
	names := a.workloads[pod(uidA)].names
	if len(names) != 2 || names[addrs[0]] != nil {
		t.Fatalf("names = %v", names)
	}
	// At most four names per address: the least recently seen goes.
	for i, n := range []string{"a.stripe.com", "b.stripe.com", "c.stripe.com", "d.stripe.com", "e.stripe.com"} {
		a.Apply(dnsEvent(100, answer(t, n, addrs[1])), at(time.Duration(10+i)*time.Second))
	}
	if l := names[addrs[1]]; len(l) != maxNamesPerAddress || slices.ContainsFunc(l, func(n dnsName) bool { return n.name == "api.stripe.com" }) {
		t.Fatalf("names for one address = %v", l)
	}
}

func TestAggregatorDNSAnswerExpiresAfterRetention(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), t0)
	a.Apply(dnsEvent(100, answer(t, "api.stripe.com", apiAddr)), at(9*time.Minute))
	a.Apply(dnsEvent(999, answer(t, "api.stripe.com", stripeAddr)), t0) // pending, names only
	a.Apply(connect(100, stripeAddr, 443, true), at(9*time.Minute))
	_, ws, _ := a.Snapshot(at(11*time.Minute), dnsRunning())
	if c := connectionTo(t, ws, uidA, stripeAddr); c.Host != "" {
		t.Fatalf("expired name kept: %+v", c)
	}
	if a.pending[999] != nil || len(a.workloads[pod(uidA)].names) != 1 {
		t.Fatalf("pending = %v, names = %v", a.pending, a.workloads[pod(uidA)].names)
	}
}

func TestAggregatorNodeHostBudget(t *testing.T) {
	b := DefaultBounds()
	b.MaxNodeHosts = 1
	a := NewAggregator(b, nil)
	a.Resync(oneScan(), t0)
	for _, addr := range []netip.Addr{stripeAddr, apiAddr} {
		a.Apply(dnsEvent(100, answer(t, "api.stripe.com", addr)), t0)
		a.Apply(connect(100, addr, 443, true), t0)
	}
	_, ws, dns := a.Snapshot(at(time.Second), dnsRunning())
	named := 0
	for _, c := range podByUID(t, ws, uidA).Connections {
		if c.Host != "" {
			named++
		}
	}
	if named != 1 || dns.State != runtimefacts.StateDegraded || dns.Reason != ReasonHostLimit {
		t.Fatalf("named = %d, dns = %+v", named, dns)
	}
}

func TestSnapshotDNSStateFromCollectorStatus(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	for _, c := range []struct {
		st   CollectorStatus
		want runtimefacts.DNSCollection
	}{
		{dnsRunning(), runtimefacts.DNSCollection{State: runtimefacts.StateRunning}},
		{CollectorStatus{DNSState: runtimefacts.StateFailed, DNSReason: "DNSAttachFailed"},
			runtimefacts.DNSCollection{State: runtimefacts.StateFailed, Reason: "DNSAttachFailed"}},
		{CollectorStatus{DNSState: runtimefacts.StateRunning, DNSDropped: 4},
			runtimefacts.DNSCollection{State: runtimefacts.StateDegraded, Reason: ReasonAnswersDropped, DroppedAnswers: 4}},
	} {
		if _, _, got := a.Snapshot(t0, c.st); got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.st, got, c.want)
		}
	}
}

func TestSnapshotDNSZeroCollectorIsNotRunning(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	for _, st := range []CollectorStatus{{}, {Failed: true, DNSState: runtimefacts.StateRunning}} {
		if _, _, dns := a.Snapshot(t0, st); dns.State != runtimefacts.StateFailed || dns.Reason != ReasonDNSNotAttached {
			t.Errorf("%+v: dns = %+v", st, dns)
		}
	}
}

// TestAggregatorSharedResetDropsNames: a workload the scan finds shared loses its names with its peers.
func TestAggregatorSharedResetDropsNames(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(100, answer(t, "api.stripe.com", stripeAddr)), t0)
	scan := oneScan()
	scan.Netns[100].Shared = true
	a.Resync(scan, at(time.Second))
	if a.workloads[pod(uidA)].names != nil {
		t.Fatal("shared workload kept its names")
	}
}

// A shared namespace's answers are dropped like its socket events: none of its
// connections is published, so no published connection loses a name and DNS
// stays Running on a node with a hostNetwork pod.
func TestAggregatorSharedNetnsAnswerIsNotUnattributed(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(dnsEvent(1, answer(t, "api.stripe.com", stripeAddr)), t0)
	if _, _, dns := a.Snapshot(at(time.Second), dnsRunning()); dns.State != runtimefacts.StateRunning {
		t.Fatalf("dns = %+v", dns)
	}
}
