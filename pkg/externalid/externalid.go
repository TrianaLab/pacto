// Package externalid names the systems outside the fleet that contracts depend
// on: vendor APIs, managed cloud resources and legacy hosts.
//
// An identity is a class-prefixed path of lower-case segments:
//
//   - a vendor is one global segment: "stripe";
//   - a managed resource is "<provider>/<service>/<account-or-project>/<region>/<id>",
//     for example "aws/rds/123456789012/eu-west-1/orders-db";
//   - an unclassified host is "host/<registrable-domain>".
//
// The same string is the fleet key, the repository path of an inferred contract
// and the CLI and MCP subject "external:<identity>". An identity carries no
// cluster component: a vendor is the same system whichever cluster calls it.
package externalid

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// ID is a canonical external identity. Build one with [Parse].
type ID string

// Reserved is the identity of the node that folds the externals beyond the
// fleet's cap. [Parse] refuses it so no contract or catalog can claim it.
const Reserved = "other-egress"

// maxLen bounds the whole identity, the length of a DNS name.
const maxLen = 253

// maxSegments is the longest form, a managed resource.
const maxSegments = 5

var segmentRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)

// Parse checks s against the identity rules and returns it as an ID.
func Parse(s string) (ID, error) {
	if s == Reserved {
		return "", fmt.Errorf("external identity %q: reserved identity for the folded node", s)
	}
	if len(s) > maxLen {
		return "", fmt.Errorf("external identity %q: longer than %d bytes", s, maxLen)
	}
	segs := strings.Split(s, "/")
	if len(segs) > maxSegments {
		return "", fmt.Errorf("external identity %q: at most %d segments", s, maxSegments)
	}
	for _, seg := range segs {
		switch {
		case seg == "":
			return "", fmt.Errorf("external identity %q: empty segment", s)
		case strings.Contains(seg, ".."):
			return "", fmt.Errorf("external identity %q: segment %q contains \"..\"", s, seg)
		case !segmentRE.MatchString(seg):
			return "", fmt.Errorf("external identity %q: segment %q must be 1 to 63 lower-case letters, digits, '.', '_' or '-', starting and ending with a letter or digit", s, seg)
		}
	}
	if segs[0] == "host" && len(segs) != 2 {
		return "", fmt.Errorf("external identity %q: a host identity has exactly two segments, host/<registrable-domain>", s)
	}
	return ID(s), nil
}

// String returns the identity text.
func (i ID) String() string { return string(i) }

// Segments returns the identity's path segments.
func (i ID) Segments() []string { return strings.Split(string(i), "/") }

// nameDrop matches what [Name] removes: anything outside the segment alphabet.
var nameDrop = regexp.MustCompile(`[^a-z0-9._-]+`)

// Name normalizes a declared dependency name into a one-segment identity, so
// consumers that spell the same name differently ("legacy-mq", "Legacy_MQ")
// join on one node. It lower-cases, turns spaces, underscores and slashes into
// '-', drops every other character outside the segment alphabet, trims
// separators at either end and then applies [Parse]. A numeric ":port" is
// dropped first, as [Host] does, so "legacy-db.corp.internal:5432" is
// "legacy-db.corp.internal". A name that normalizes to
// nothing, to a segment [Parse] refuses (longer than 63 bytes, for example) or
// to [Reserved] is an error.
func Name(s string) (ID, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	if host, port, err := net.SplitHostPort(n); err == nil && portRE.MatchString(port) {
		n = host
	}
	n = strings.NewReplacer(" ", "-", "_", "-", "/", "-").Replace(n)
	n = strings.Trim(nameDrop.ReplaceAllString(n, ""), "-._")
	if n == "" {
		return "", fmt.Errorf("external name %q: nothing is left after normalizing it", s)
	}
	return Parse(n)
}

// portRE is a numeric port.
var portRE = regexp.MustCompile(`^[0-9]+$`)

// dnsNameRE is a lower-case DNS name of at least two labels.
var dnsNameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Host names an unclassified host by its registrable domain:
// "mq.corp-legacy.co.uk" is "host/corp-legacy.co.uk". It accepts an optional
// trailing dot and ":port" and reports false for anything that is not a DNS
// name under an ICANN public suffix (an IP address, a single label, a private
// or internal suffix such as "herokuapp.com" or "corp.internal").
func Host(s string) (ID, bool) {
	h := strings.ToLower(strings.TrimSpace(s))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	if !dnsNameRE.MatchString(h) {
		return "", false
	}
	if _, icann := publicsuffix.PublicSuffix(h); !icann {
		return "", false
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return "", false
	}
	id, err := Parse("host/" + d)
	return id, err == nil
}
