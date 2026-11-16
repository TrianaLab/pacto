package externalcatalog

import (
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// How sure a [Match] is.
const (
	// ConfidenceLikely is a host rule or an OTel system value that matched.
	ConfidenceLikely = "likely"
	// ConfidencePossible is a configuration key name that matched, with
	// nothing else to confirm it.
	ConfidencePossible = "possible"
)

// What a [Match] matched on.
const (
	KindHost    = "host"
	KindKeyName = "keyName"
	// KindSystem is an OpenTelemetry db.system, messaging.system or
	// rpc.system value.
	KindSystem = "system"
)

// Match is one external system a configuration value, a key name or an
// observed name points to. It carries the normalized host and port at most,
// never the value it was read from.
type Match struct {
	Identity   externalid.ID `json:"identity"`
	Class      string        `json:"class"`
	Confidence string        `json:"confidence"`
	Kind       string        `json:"kind"`
	// Host is the host, with ":port" when one was given, for a host match.
	Host string `json:"host,omitempty"`
	// Entry is the catalog entry that matched, empty for an unclassified host.
	Entry string `json:"entry,omitempty"`
}

// HostPort is one host a value names, with no userinfo, path, query or
// fragment.
type HostPort struct {
	Host, Port string
	// Scheme reports whether the value carried "://".
	Scheme bool
}

// String returns "host" or "host:port".
func (h HostPort) String() string {
	if h.Port == "" {
		return h.Host
	}
	return net.JoinHostPort(h.Host, h.Port)
}

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeHost reads the hosts a configuration value names: a URL, a JDBC
// URL, a host:port or a comma-separated seed list. It keeps the lower-cased
// host and port and drops everything else. A value that does not parse, an IP
// address, a single label, localhost, a name with a "svc" label, a
// "*.cluster.local" name and a name with a label outside RFC 1123 give
// nothing: in-cluster names are dependencies and IPs are egress.
func NormalizeHost(raw string) []HostPort {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "jdbc:")
	scheme := strings.Contains(raw, "://")
	var out []HostPort
	for i, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if i > 0 || !scheme {
			part = "tcp://" + part
		}
		u, err := url.Parse(part)
		if err != nil {
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		if externalHost(host) {
			out = append(out, HostPort{Host: host, Port: u.Port(), Scheme: scheme})
		}
	}
	return out
}

// externalHost reports whether a lower-cased host can name a system outside
// the cluster.
func externalHost(host string) bool {
	if net.ParseIP(host) != nil || len(host) > 253 || strings.HasSuffix(host, ".cluster.local") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "svc" || !labelRE.MatchString(l) {
			return false
		}
	}
	return true
}

// MatchHost names the system a host belongs to. The longest catalog host rule
// wins, an exact host over a "*." wildcard of the same length. Without one, a
// name of three or more labels is the unclassified "host/<registrable-domain>"
// (see [externalid.Host]). A two-label name matches only a catalog rule,
// because a namespace short form ("orders.shop") reads like a vendor's apex
// domain.
func (c *Catalog) MatchHost(h HostPort) (Match, bool) {
	if e, ok := c.bestEntry(func(e Entry) int { return hostScore(e.Hosts, h.Host) }); ok {
		return Match{Identity: e.Identity, Class: e.Class, Confidence: ConfidenceLikely, Kind: KindHost, Host: h.String(), Entry: string(e.Identity)}, true
	}
	if strings.Count(h.Host, ".") < 2 {
		return Match{}, false
	}
	id, ok := externalid.Host(h.Host)
	if !ok {
		return Match{}, false
	}
	return Match{Identity: id, Class: ClassUnclassified, Confidence: ConfidenceLikely, Kind: KindHost, Host: h.String()}, true
}

// MatchKeyName names the system a configuration key name suggests, compared
// without case against the catalog's key patterns, with "-" and "." read as
// "_" so a Secret name (stripe-credentials) reads like an env name. The match
// is only possible: a key name alone confirms nothing.
func (c *Catalog) MatchKeyName(key string) (Match, bool) {
	key = strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(key))
	e, ok := c.bestEntry(func(e Entry) int { return keyScore(e.KeyPatterns, key) })
	if !ok {
		return Match{}, false
	}
	return Match{Identity: e.Identity, Class: e.Class, Confidence: ConfidencePossible, Kind: KindKeyName, Entry: string(e.Identity)}, true
}

// MatchOTel names the system an OpenTelemetry db.system, messaging.system or
// rpc.system value names, compared exactly without case against the entries'
// otel values. A protocol no entry claims (grpc) gives no match.
func (c *Catalog) MatchOTel(value string) (Match, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	e, ok := c.bestEntry(func(e Entry) int {
		if slices.ContainsFunc(slices.Concat(e.OTel.DBSystem, e.OTel.MessagingSystem, e.OTel.RPCSystem), func(v string) bool { return strings.ToLower(v) == value }) {
			return 1
		}
		return 0
	})
	if !ok {
		return Match{}, false
	}
	return Match{Identity: e.Identity, Class: e.Class, Confidence: ConfidenceLikely, Kind: KindSystem, Entry: string(e.Identity)}, true
}

// bestEntry returns the entry with the highest positive score, the lower
// identity on a tie so the answer never depends on map order.
func (c *Catalog) bestEntry(score func(Entry) int) (Entry, bool) {
	var best Entry
	bestScore := 0
	for _, s := range c.entries {
		n := score(s.entry)
		if n > bestScore || (n == bestScore && n > 0 && s.entry.Identity < best.Identity) {
			best, bestScore = s.entry, n
		}
	}
	return best, bestScore > 0
}

// hostScore is the length of the longest rule that matches host, plus one for
// an exact rule, or 0.
func hostScore(rules []string, host string) int {
	best := 0
	for _, r := range rules {
		n := 0
		if suffix, ok := strings.CutPrefix(r, "*"); ok {
			if strings.HasSuffix(host, suffix) {
				n = len(suffix)
			}
		} else if host == r {
			n = len(r) + 1
		}
		best = max(best, n)
	}
	return best
}

// keyScore is the length of the longest pattern that matches an upper-cased
// key, plus one for an exact pattern, or 0.
func keyScore(patterns []string, key string) int {
	best := 0
	for _, p := range patterns {
		n := 0
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if strings.HasPrefix(key, prefix) {
				n = len(prefix)
			}
		} else if key == p {
			n = len(p) + 1
		}
		best = max(best, n)
	}
	return best
}

// hostKeyRE is a key name that says its value is a host. A DSN key never
// reaches it: schemax.SensitiveKey holds "dsn", so its value is never parsed.
var hostKeyRE = regexp.MustCompile(`(?i)(url|uri|host|endpoint|addr|address|server|broker)`)

// Classify names the external systems a configuration value and its key point
// to. A value under a sensitive key is never parsed. Otherwise every host the
// value names is a candidate when the value had a scheme or a port, or the key
// says it holds a host, and each candidate goes through [Catalog.MatchHost].
// When no host matched, the key name alone may give a possible match. The
// result holds one match per identity, the strongest. A nil catalog gives nil.
func (c *Catalog) Classify(value, key string) []Match {
	if c == nil {
		return nil
	}
	var out []Match
	if !schemax.SensitiveKey(key) {
		hostKey := hostKeyRE.MatchString(key)
		for _, h := range NormalizeHost(value) {
			if !h.Scheme && h.Port == "" && !hostKey {
				continue
			}
			if m, ok := c.MatchHost(h); ok {
				out = append(out, m)
			}
		}
	}
	if len(out) == 0 && key != "" {
		if m, ok := c.MatchKeyName(key); ok {
			out = append(out, m)
		}
	}
	return dedupe(out)
}

// dedupe keeps one match per identity, in first-seen order: the first likely
// one, else the first.
func dedupe(ms []Match) []Match {
	at := map[externalid.ID]int{}
	var out []Match
	for _, m := range ms {
		i, seen := at[m.Identity]
		switch {
		case !seen:
			at[m.Identity] = len(out)
			out = append(out, m)
		case m.Confidence == ConfidenceLikely && out[i].Confidence != ConfidenceLikely:
			out[i] = m
		}
	}
	return out
}
