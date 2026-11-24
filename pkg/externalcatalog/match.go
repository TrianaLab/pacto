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
	// KindSDK is a client library in a dependency manifest.
	KindSDK = "sdk"
	// KindResource is a Terraform resource type or a Crossplane kind that
	// provisions a managed service.
	KindResource = "resource"
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
	// Origin is the catalog that supplied Entry (default or organization),
	// empty for an unclassified host.
	Origin string `json:"origin,omitempty"`
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
	if e, origin, ok := c.bestEntry(func(e Entry) int { return hostScore(e.Hosts, h.Host) }); ok {
		m := entryMatch(e, origin, ConfidenceLikely, KindHost)
		m.Host = h.String()
		return m, true
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

// NamespaceShortForm reports whether m is an unclassified host match (no
// catalog rule) whose last label inNS names as a namespace: the in-cluster
// short form <pod>.<service>.<namespace>, which resolves through the cluster
// search domain and only reads like a registrable domain when the namespace
// is also a top-level domain (dev, app, prod). A caller that knows the
// cluster's namespaces drops such a match.
func NamespaceShortForm(m Match, inNS func(string) bool) bool {
	if m.Kind != KindHost || m.Entry != "" {
		return false
	}
	host := m.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return inNS(host[strings.LastIndex(host, ".")+1:])
}

// MatchKeyName names the system a configuration key name suggests, compared
// without case against the catalog's key patterns, with "-" and "." read as
// "_" so a Secret name (stripe-credentials) reads like an env name. The match
// is only possible: a key name alone confirms nothing.
func (c *Catalog) MatchKeyName(key string) (Match, bool) {
	key = strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(key))
	e, origin, ok := c.bestEntry(func(e Entry) int { return keyScore(e.KeyPatterns, key) })
	if !ok {
		return Match{}, false
	}
	return entryMatch(e, origin, ConfidencePossible, KindKeyName), true
}

// MatchOTel names the system an OpenTelemetry db.system, messaging.system or
// rpc.system value names, compared exactly without case against the entries'
// otel values. A protocol no entry claims (grpc) gives no match.
func (c *Catalog) MatchOTel(value string) (Match, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	e, origin, ok := c.bestEntry(func(e Entry) int {
		return boolScore(slices.ContainsFunc(slices.Concat(e.OTel.DBSystem, e.OTel.MessagingSystem, e.OTel.RPCSystem), func(v string) bool { return strings.ToLower(v) == value }))
	})
	if !ok {
		return Match{}, false
	}
	return entryMatch(e, origin, ConfidenceLikely, KindSystem), true
}

// pypiRunRE is what PEP 503 folds to one '-' in a PyPI name.
var pypiRunRE = regexp.MustCompile(`[-_.]+`)

// MatchSDK names the system a client library belongs to. The ecosystem is
// go, npm, pypi or maven; name is the module or package as a manifest writes
// it ("github.com/stripe/stripe-go/v76", "Stripe", "com.stripe:stripe-java").
// A Go module drops a trailing /vN and matches a listed module or any
// sub-path of one, the longest listed module winning. A PyPI name is
// normalized first. The match is only possible: a library in a manifest
// proves no call.
func (c *Catalog) MatchSDK(ecosystem, name string) (Match, bool) {
	var score func(Entry) int
	switch ecosystem {
	case "go":
		name = goMajorRE.ReplaceAllString(name, "")
		score = func(e Entry) int { return goModuleScore(e.SDKModules, name) }
	case "npm", "maven":
		row := ecosystem + ":" + name
		score = func(e Entry) int { return boolScore(slices.Contains(e.SDKModules, row)) }
	case "pypi":
		row := "pypi:" + pypiRunRE.ReplaceAllString(strings.ToLower(name), "-")
		score = func(e Entry) int { return boolScore(slices.Contains(e.SDKModules, row)) }
	default:
		return Match{}, false
	}
	e, origin, ok := c.bestEntry(score)
	if !ok {
		return Match{}, false
	}
	return entryMatch(e, origin, ConfidencePossible, KindSDK), true
}

// goModuleScore is the length of the longest listed Go module that is mod or
// a parent path of it, or 0. An ecosystem-prefixed row never matches, since
// a Go module path holds no ':'.
func goModuleScore(rows []string, mod string) int {
	best := 0
	for _, r := range rows {
		if mod == r || strings.HasPrefix(mod, r+"/") {
			best = max(best, len(r))
		}
	}
	return best
}

// MatchTerraform names the managed service a Terraform resource type
// provisions and returns the id attribute the catalog lists for it, empty
// when the row names none. The match is only possible: a resource in a
// repository proves the team provisions it, not that a service calls it.
func (c *Catalog) MatchTerraform(resourceType string) (Match, string, bool) {
	e, origin, ok := c.bestEntry(func(e Entry) int {
		return boolScore(slices.ContainsFunc(e.Terraform, func(r string) bool { return terraformType(r) == resourceType }))
	})
	if !ok {
		return Match{}, "", false
	}
	var attr string
	for _, r := range e.Terraform {
		if t, a, _ := strings.Cut(r, ":"); t == resourceType {
			attr = a
		}
	}
	return entryMatch(e, origin, ConfidencePossible, KindResource), attr, true
}

// MatchCrossplane names the managed service a Crossplane kind provisions,
// compared exactly on "<group>/<Kind>". The match is only possible.
func (c *Catalog) MatchCrossplane(group, kind string) (Match, bool) {
	row := group + "/" + kind
	e, origin, ok := c.bestEntry(func(e Entry) int { return boolScore(slices.Contains(e.Crossplane, row)) })
	if !ok {
		return Match{}, false
	}
	return entryMatch(e, origin, ConfidencePossible, KindResource), true
}

// MatchInventory names the managed service a cloud inventory resource type
// belongs to: exact for AWS and GCP types (GCP asset types are
// case-sensitive), case-insensitive for Azure types, which Resource Graph
// reports in any case. The match is only possible: an account holding a
// resource does not prove a service calls it.
func (c *Catalog) MatchInventory(resourceType string) (Match, bool) {
	e, origin, ok := c.bestEntry(func(e Entry) int {
		return boolScore(slices.ContainsFunc(e.Inventory, func(r string) bool {
			return r == resourceType || (strings.HasPrefix(r, "microsoft.") && strings.EqualFold(r, resourceType))
		}))
	})
	if !ok {
		return Match{}, false
	}
	return entryMatch(e, origin, ConfidencePossible, KindResource), true
}

// entryMatch is the match of catalog entry e, supplied by origin.
func entryMatch(e Entry, origin, confidence, kind string) Match {
	return Match{Identity: e.Identity, Class: e.Class, Confidence: confidence, Kind: kind, Entry: string(e.Identity), Origin: origin}
}

func boolScore(b bool) int {
	if b {
		return 1
	}
	return 0
}

// bestEntry returns the entry with the highest positive score and the origin
// of the catalog that supplied it, the lower identity on a tie so the answer
// never depends on map order.
func (c *Catalog) bestEntry(score func(Entry) int) (Entry, string, bool) {
	var best sourced
	bestScore := 0
	for _, s := range c.entries {
		n := score(s.entry)
		if n > bestScore || (n == bestScore && n > 0 && s.entry.Identity < best.entry.Identity) {
			best, bestScore = s, n
		}
	}
	if bestScore == 0 {
		return Entry{}, "", false
	}
	return best.entry, c.refs[best.ref].Origin, true
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
