/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"net/netip"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/publicsuffix"
)

// maxHostLen is the longest DNS name in text form, without the trailing dot.
const maxHostLen = 253

// parseDNSAnswer reads one DNS response a workload received: the question name and every A and AAAA
// address of the answer section (a CNAME chain is followed by keeping the question, which is the name the
// workload asked for). Only successful, complete responses count, and only a lowercase LDH hostname
// outside the cluster's own Service and pod zones is kept: any pod can feed its own socket a forged
// answer, so the name must stay one JSON byte per character and short.
func parseDNSAnswer(payload []byte) (name string, addrs []netip.Addr, ok bool) {
	var p dnsmessage.Parser
	h, err := p.Start(payload)
	if err != nil || !h.Response || h.Truncated || h.RCode != dnsmessage.RCodeSuccess {
		return "", nil, false
	}
	q, err := p.Question()
	if err != nil {
		return "", nil, false
	}
	name, ok = ldhHost(q.Name.String())
	if !ok || clusterName(name) || p.SkipAllQuestions() != nil {
		return "", nil, false
	}
	addrs = answerAddrs(&p)
	return name, addrs, len(addrs) > 0
}

// answerAddrs collects the A and AAAA addresses of the answer section, stopping at the first record it
// cannot read.
func answerAddrs(p *dnsmessage.Parser) []netip.Addr {
	var addrs []netip.Addr
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return addrs
		}
		var addr netip.Addr
		switch h.Type {
		case dnsmessage.TypeA:
			r, rerr := p.AResource()
			addr, err = netip.AddrFrom4(r.A), rerr
		case dnsmessage.TypeAAAA:
			r, rerr := p.AAAAResource()
			addr, err = netip.AddrFrom16(r.AAAA).Unmap(), rerr
		default:
			err = p.SkipAnswer()
		}
		if err != nil {
			return addrs
		}
		if addr.IsValid() {
			addrs = append(addrs, addr)
		}
	}
}

// ldhHost lowercases a DNS name, strips its trailing dot and accepts it only when every label is 1 to 63
// letters, digits or hyphens and the whole name is at most 253 bytes.
func ldhHost(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" || len(name) > maxHostLen {
		return "", false
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > 63 || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return "", false
		}
	}
	return name, true
}

// clusterName reports whether a name is an in-cluster Service or pod name (<svc>.<ns>.svc.<domain>,
// <ip>.<ns>.pod.<domain>): a call to it never leaves the cluster.
func clusterName(name string) bool {
	for label := range strings.SplitSeq(name, ".") {
		if label == "svc" || label == "pod" {
			return true
		}
	}
	return false
}

// registrableDomain is the name's eTLD+1, or the name itself when it has none.
func registrableDomain(name string) string {
	if d, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return d
	}
	return name
}
