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
	"regexp"
	"strings"
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
