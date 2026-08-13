// Command productready blocks until the LIVE dashboard's Product API proves the
// Kind operational-graph fixture is fully assembled, then prints the canonical
// entity keys it discovered as JSON.
//
// It exists so the shell harness stays THIN. Everything semantic about the
// fixture — which revisions must be retrievable, which target must link to which
// revision, whether the declared orders -> checkout edge was corroborated by the
// managed observation source — is a typed decode of the real product answer here,
// not a `curl | grep` over a raw snapshot. The shell only starts a port-forward
// and runs this.
//
// It NEVER constructs a key. A ServiceKey is domain-escaped, a RevisionKey is a
// ServiceKey with a content id, a TargetKey is scope/kind/name: reproducing those
// escapes in a test would be a second implementation of the identity rules that
// could agree with itself while disagreeing with the product. Every key is
// DISCOVERED through /api/fleet/entities and handed on verbatim — to the checks
// below and, via -out, to the browser journeys, so both layers address exactly
// the entities the product published.
//
// The wait is a poll, not a sleep: the dashboard rebuilds its snapshot on a 30s
// interval, so the fixture becomes true at an unpredictable moment. Every round
// re-checks EVERY fact and accumulates all outstanding failures, so a timeout
// reports the complete list of what never became true rather than the first thing
// that happened to be missing.
//
// A round is also ONE SNAPSHOT. The Manager can swap the snapshot between any two
// requests, so a round that reads its service list from one snapshot and its
// neighborhood from the next has proved nothing about either: it has spliced two
// system views and called the result coherent. Every Product response carries the
// id of the snapshot that answered it; the FIRST response of a round adopts that
// id and every later response must repeat it, or the whole round is discarded and
// retried. The id handed to the browser journeys is therefore the id that proved
// every fact, not an id read afterwards from a request that could have landed on a
// different snapshot entirely.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Decoding targets are the route-neutral pkg/fleet types, not the dashboard
// transport DTOs: the transport only ADDS hrefs, so the fleet types decode the
// same payload without dragging client-go into a test binary.

func main() {
	var (
		base      = flag.String("base", "http://127.0.0.1:8080", "dashboard base URL")
		domain    = flag.String("domain", "", "OCI domain the fixture services live in (registry host + org)")
		checkout  = flag.String("checkout", "checkout", "provider service name")
		orders    = flag.String("orders", "orders", "consumer service name")
		revA      = flag.String("checkout-a", "1.0.0", "deployed checkout revision (version A)")
		revB      = flag.String("checkout-b", "1.1.0", "published-but-undeployed checkout revision (version B)")
		ordersRev = flag.String("orders-version", "1.0.0", "orders revision version")
		ociSrc    = flag.String("oci-source", "oci", "expected OCI data source id")
		cacheSrc  = flag.String("cache-source", "cache", "expected on-disk OCI cache data source id")
		obsSrc    = flag.String("observation-source", "orders-traces", "expected managed observation data source id")
		evSrc     = flag.String("evidence-source", "evidence-http", "expected Evidence Server data source id")
		evService = flag.String("evidence-service", "payments", "service whose target arrives via the Evidence Server")
		timeout   = flag.Duration("timeout", 6*time.Minute, "how long to wait for the fixture to become true")
		interval  = flag.Duration("interval", 5*time.Second, "poll interval")
		snapshots = flag.Int("snapshots", 1, "how many DISTINCT snapshots must each prove the whole fixture")
		outPath   = flag.String("out", "", "write the discovered canonical keys here as JSON")
	)
	flag.Parse()
	if *domain == "" {
		exit("-domain is required (the registry host + org the fixture publishes to)")
	}
	if *snapshots < 1 {
		exit("-snapshots must be at least 1")
	}

	p := &prober{
		base:      strings.TrimSuffix(*base, "/"),
		http:      &http.Client{Timeout: 20 * time.Second},
		interval:  *interval,
		timeout:   *timeout,
		snapshots: *snapshots,
		want: fixture{
			Domain: *domain, Checkout: *checkout, Orders: *orders,
			CheckoutA: *revA, CheckoutB: *revB, OrdersVersion: *ordersRev,
			OCISource: *ociSrc, CacheSource: *cacheSrc, ObservationSource: *obsSrc,
			EvidenceSource: *evSrc, EvidenceService: *evService,
		},
	}

	keys, problems := p.run(os.Stdout)
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "the live Product API never proved the fixture within %s:\n", *timeout)
		for _, pr := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", pr)
		}
		os.Exit(1)
	}
	emit(keys, *outPath)
}

// run polls until the fixture is proven, and returns the keys of the LAST
// snapshot that proved it. A non-empty problem list means nothing was proven and
// the caller must not emit keys: a round that failed for any reason — including
// having read its facts from two different snapshots — has no fixture to hand on.
//
// The POST-CACHE state is proved by the facts, not by counting refreshes. The
// pod's OCI cache starts empty and the first refresh's registry pulls are what
// fill it, so the state under test — registry and disk cache offering the same
// published artifacts — only exists later. Waiting for N distinct snapshots
// cannot establish it: SnapshotID hashes the generation time, so distinct ids
// prove only that time passed. What proves it is fact 3 and facts 5-7, which
// require the cache source to be usable and every fixture revision to carry BOTH
// sources in its provenance. -snapshots N still requires N distinct snapshots to
// each prove the whole fixture, so the post-cache state must also be STABLE
// across a refresh rather than true once.
func (p *prober) run(w io.Writer) (discovered, []string) {
	deadline := time.Now().Add(p.timeout)
	proven := map[string]bool{}
	var last discovered
	for round := 1; ; round++ {
		keys, problems := p.probe()
		if len(problems) == 0 {
			last = keys
			if !proven[keys.SnapshotID] {
				proven[keys.SnapshotID] = true
				_, _ = fmt.Fprintf(w, "  PASS: snapshot %s proves the fixture (%d facts, round %d; %d of %d distinct snapshots)\n",
					keys.SnapshotID, factCount, round, len(proven), p.snapshots)
			}
			if len(proven) >= p.snapshots {
				return last, nil
			}
			problems = []string{fmt.Sprintf(
				"only %d of %d distinct snapshots have proved the fixture; waiting for the next dashboard refresh",
				len(proven), p.snapshots)}
		}
		if !time.Now().Before(deadline) {
			return discovered{}, problems
		}
		_, _ = fmt.Fprintf(w, "  waiting: %d of %d facts outstanding (first: %s)\n", len(problems), factCount, problems[0])
		time.Sleep(p.interval)
	}
}

// factCount is the number of facts section 8 requires the gate to prove. It is a
// constant so the progress line reports a denominator that cannot drift silently
// with the number of failures a round happens to produce. Fact 14 is the round's
// snapshot coherence: the other thirteen are only facts about the fleet if one
// snapshot answered all of them.
const factCount = 14

func exit(msg string) {
	fmt.Fprintln(os.Stderr, "productready:", msg)
	os.Exit(2)
}

func emit(k discovered, path string) {
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		exit(err.Error())
	}
	if path == "" {
		fmt.Println(string(b))
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		exit(err.Error())
	}
	fmt.Printf("  wrote the discovered canonical keys to %s\n", path)
}

// fixture is what the harness published; every field is a NAME or an id the
// shell configured, never a key.
type fixture struct {
	Domain, Checkout, Orders            string
	CheckoutA, CheckoutB, OrdersVersion string
	OCISource, CacheSource              string
	ObservationSource                   string
	EvidenceSource, EvidenceService     string
}

// discovered is the canonical identity the product published for each fixture
// entity, handed to the browser journeys verbatim.
type discovered struct {
	SnapshotID        string `json:"snapshotId"`
	Domain            string `json:"domain"`
	CheckoutService   string `json:"checkoutService"`
	OrdersService     string `json:"ordersService"`
	EvidenceService   string `json:"evidenceService"`
	CheckoutRevisionA string `json:"checkoutRevisionA"`
	CheckoutRevisionB string `json:"checkoutRevisionB"`
	OrdersRevision    string `json:"ordersRevision"`
	CheckoutTarget    string `json:"checkoutTarget"`
	OrdersTarget      string `json:"ordersTarget"`
	EvidenceTarget    string `json:"evidenceTarget"`
	OCISource         string `json:"ociSource"`
	CacheSource       string `json:"cacheSource"`
	ObservationSource string `json:"observationSource"`
	EvidenceSource    string `json:"evidenceSource"`
	// Versions travel with the keys so the browser can drive the change-analysis
	// pickers by their real option labels ("<service> <version>").
	CheckoutVersionA string `json:"checkoutVersionA"`
	CheckoutVersionB string `json:"checkoutVersionB"`
	OrdersVersion    string `json:"ordersVersion"`
	CheckoutName     string `json:"checkoutName"`
	OrdersName       string `json:"ordersName"`
}

type prober struct {
	base      string
	http      *http.Client
	want      fixture
	interval  time.Duration
	timeout   time.Duration
	snapshots int

	// problems accumulates every unmet fact of the current round.
	problems []string
	// snapshotID is the id this round adopted from its first Product response;
	// mixed records that some response of this round disagreed with it.
	snapshotID string
	mixed      bool
}

func (p *prober) failf(format string, a ...any) {
	p.problems = append(p.problems, fmt.Sprintf(format, a...))
}

// maxBody bounds one Product response. The gate reads a payload twice (once for
// its snapshot id, once for its content), so the body is buffered; a runaway
// response must not become a runaway allocation in the harness.
const maxBody = 32 << 20

func (p *prober) get(path string, q url.Values, out any) error {
	u := p.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	resp, err := p.http.Get(u) //nolint:noctx // a poll round is bounded by the client timeout
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("GET %s: reading the body: %w", u, err)
	}
	// Every Product response carries the meta of the snapshot that answered it.
	// Decoding it separately keeps the coherence rule in ONE place instead of
	// asking each of the six typed decode targets to surface its own meta.
	var env struct {
		Meta fleet.ProductMeta `json:"meta"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("GET %s: decoding the product meta: %w", u, err)
	}
	if err := p.adopt(env.Meta.SnapshotID, u); err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// adopt binds the round to exactly one snapshot.
//
// The dashboard Manager replaces its snapshot on a refresh interval, and every
// request is served by whichever snapshot is current when it arrives. Reading a
// dozen facts across a dozen requests therefore proves the fixture only if one
// snapshot answered all of them; otherwise the gate can pass on a system view
// that never existed — the service list from before a refresh, the neighborhood
// from after it. The first response of a round fixes the id; every later response
// must match it. An answer with NO id is equally fatal: an unidentified snapshot
// cannot be shown to be the same one.
func (p *prober) adopt(id, u string) error {
	if id == "" {
		p.mixed = true
		return fmt.Errorf("GET %s carries no snapshot id; a fact from an unidentified snapshot proves nothing", u)
	}
	if p.snapshotID == "" {
		p.snapshotID = id
		return nil
	}
	if id != p.snapshotID {
		p.mixed = true
		return fmt.Errorf("GET %s was answered by snapshot %s, but this round adopted %s; the dashboard refreshed mid-round, so these facts are not one system view",
			u, id, p.snapshotID)
	}
	return nil
}

// list pages one entity kind, optionally scoped to a parent service.
func (p *prober) list(kind, service string) []fleet.EntityRef {
	q := url.Values{"kinds": {kind}, "limit": {"200"}}
	if service != "" {
		q.Set("service", service)
	}
	var l fleet.EntityList
	if err := p.get("/api/fleet/entities", q, &l); err != nil {
		p.failf("listing %s entities: %v", kind, err)
		return nil
	}
	return l.Entities
}

func (p *prober) detail(kind, key string) *fleet.EntityDetail {
	var d fleet.EntityDetail
	if err := p.get("/api/fleet/entities/"+kind, url.Values{"key": {key}}, &d); err != nil {
		p.failf("reading %s %q: %v", kind, key, err)
		return nil
	}
	return &d
}

func (p *prober) probe() (discovered, []string) {
	p.problems = nil
	p.snapshotID = ""
	p.mixed = false
	got := discovered{
		Domain: p.want.Domain, CheckoutName: p.want.Checkout, OrdersName: p.want.Orders,
		CheckoutVersionA: p.want.CheckoutA, CheckoutVersionB: p.want.CheckoutB,
		OrdersVersion: p.want.OrdersVersion,
	}

	// Facts 1-3: the three data sources exist AND answered in full. A source that
	// is merely PRESENT is not usable: a partial OCI source means the revisions
	// below may be missing for a reason the fixture would otherwise hide.
	//
	// The CACHE source is here because the state this gate must reach is the
	// POST-CACHE one: the pod's OCI cache starts empty and the registry pulls of
	// the first refresh are what fill it, so only a later snapshot has the registry
	// and the disk cache offering the same published artifacts at once. That is the
	// pairing that used to publish one artifact as two revisions, and it is proved
	// below by the provenance of the revisions themselves — this fact only
	// establishes that the source contributing it was usable at all.
	sources := p.list("source", "")
	got.OCISource = p.usableSource(sources, p.want.OCISource)
	got.CacheSource = p.usableSource(sources, p.want.CacheSource)
	got.ObservationSource = p.usableSource(sources, p.want.ObservationSource)
	// The Evidence Server source is not a fact on its own — fact 13 is the target it
	// produced — but its id has to be discovered to attribute that target to it.
	got.EvidenceSource = p.want.EvidenceSource

	// Fact 4: both fixture services exist, in the OCI domain (a K8s-synthetic
	// service would land in a different domain and would not carry OCI revisions).
	services := p.list("service", "")
	got.CheckoutService = p.serviceKey(services, p.want.Checkout, p.want.Domain)
	got.OrdersService = p.serviceKey(services, p.want.Orders, p.want.Domain)
	got.EvidenceService = p.serviceKey(services, p.want.EvidenceService, "")

	// Facts 5-7: three real contract revisions, each ONE canonical, exact,
	// retrievable revision contributed by BOTH the registry and the disk cache.
	got.CheckoutRevisionA = p.revisionKey(got.CheckoutService, p.want.CheckoutA)
	got.CheckoutRevisionB = p.revisionKey(got.CheckoutService, p.want.CheckoutB)
	got.OrdersRevision = p.revisionKey(got.OrdersService, p.want.OrdersVersion)

	// Facts 8-9: the running targets link to the revisions actually deployed.
	got.CheckoutTarget = p.runningTarget(got.CheckoutService, got.CheckoutRevisionA)
	got.OrdersTarget = p.runningTarget(got.OrdersService, got.OrdersRevision)

	// Facts 10-12: declared, observed, and reconciled as one edge.
	p.reconciledEdge(got.OrdersService, got.CheckoutService)

	// Fact 13: the external target still arrives over the Evidence Server.
	got.EvidenceTarget = p.evidenceTarget(got.EvidenceService)

	// Fact 14: all thirteen came out of ONE snapshot. Each mismatching response has
	// already reported itself, but the verdict is stated over the round as a whole
	// so a splice can never be reduced to a single recoverable-looking read — and
	// so a round that somehow swallowed the per-request error still fails here.
	switch {
	case p.snapshotID == "":
		p.failf("no product response identified the snapshot it came from; the round proves nothing")
	case p.mixed:
		p.failf("the round read its facts from more than one snapshot (adopted %s); discarding it and retrying", p.snapshotID)
	}
	// The id handed on is the one that proved every fact above — never one read
	// afterwards, which could come from a snapshot none of the facts were read from.
	got.SnapshotID = p.snapshotID
	return got, p.problems
}

func (p *prober) usableSource(sources []fleet.EntityRef, id string) string {
	for _, s := range sources {
		if s.Key != id {
			continue
		}
		d := p.detail("source", id)
		if d == nil {
			return ""
		}
		if d.Source == nil {
			p.failf("data source %q has no source detail", id)
			return ""
		}
		if d.Source.Health != "available" {
			p.failf("data source %q is %q, not available", id, d.Source.Health)
			return ""
		}
		return id
	}
	p.failf("data source %q is not in the snapshot (present: %s)", id, keysOf(sources))
	return ""
}

func (p *prober) serviceKey(services []fleet.EntityRef, name, domain string) string {
	var hits []fleet.EntityRef
	for _, s := range services {
		if s.Label == name && (domain == "" || s.Domain == domain) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].Key
	case 0:
		p.failf("service %q is not in the snapshot for domain %q (present: %s)", name, domain, keysOf(services))
	default:
		// Two services with one name is the duplicate-identity hazard, not a pass:
		// an observed edge could not be attributed unambiguously.
		p.failf("service %q resolves to %d services in domain %q (%s)", name, len(hits), domain, keysOf(hits))
	}
	return ""
}

// revisionKey finds one version of one service and proves its identity is the
// canonical, immutable, retrievable one — the whole point of publishing the
// fixture through a real registry rather than inline in a CR.
//
// EXACTLY ONE revision may carry the version. The fixture publishes each version
// once, so a second revision at the same version is not an alternative to pick
// between: it is one published artifact that entered the fleet under two
// identities — the shape the dashboard produced when the registry source and the
// disk-cache source disagreed about what a cached bundle was. Scanning for the
// first retrievable match would step over exactly that record and pass.
//
// And that one revision must have been contributed by BOTH the registry and the
// disk cache. Uniqueness alone is satisfied trivially before the cache is ever
// populated, so a gate that stopped there would report the pre-cache fleet as
// proof about the post-cache one. Two sources on one revision is the LIVE
// statement that the pairing happened and collapsed to a single identity —
// something no count of distinct snapshots can establish.
func (p *prober) revisionKey(serviceKey, version string) string {
	if serviceKey == "" {
		return ""
	}
	revs := p.list("revision", serviceKey)
	var hits []fleet.EntityRef
	for _, r := range revs {
		if r.Version == version {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 1:
	case 0:
		p.failf("service %s has no revision %s (present: %s)", serviceKey, version, versionsOf(revs))
		return ""
	default:
		p.failf("service %s has %d revisions at version %s (%s); one published artifact must be one canonical revision",
			serviceKey, len(hits), version, keysOf(hits))
		return ""
	}
	r := hits[0]
	d := p.detail("revision", r.Key)
	if d == nil || d.Revision == nil {
		p.failf("revision %s %s has no revision detail", serviceKey, version)
		return ""
	}
	id := d.Revision.Identity
	if id.IdentityClass != "exact" {
		p.failf("revision %s %s has identity class %q, not exact (resolvedRef %q)", serviceKey, version, id.IdentityClass, id.ResolvedRef)
		return ""
	}
	if !id.Retrievable {
		p.failf("revision %s %s is not retrievable", serviceKey, version)
		return ""
	}
	if !p.contributedByRegistryAndCache(serviceKey, version, d.Revision.Provenance) {
		return ""
	}
	return r.Key
}

// contributedByRegistryAndCache proves the revision is the SAME record the
// registry source and the disk-cache source both arrived at.
//
// A truncated source list is a failure, not a maybe: the check is about which
// sources contributed, and a preview that dropped some cannot answer it.
func (p *prober) contributedByRegistryAndCache(serviceKey, version string, prov fleet.RevisionProvenance) bool {
	if prov.Sources.Truncated {
		p.failf("revision %s %s lists %d of %d provenance sources; the truncated list cannot show which contributed",
			serviceKey, version, prov.Sources.Count, prov.Sources.Total)
		return false
	}
	for _, want := range []string{p.want.OCISource, p.want.CacheSource} {
		if !sliceHas(prov.Sources.Items, want) {
			p.failf("revision %s %s was not contributed by %q (sources: %s); the registry and the disk cache must reach the SAME canonical revision",
				serviceKey, version, want, strings.Join(prov.Sources.Items, ", "))
			return false
		}
	}
	return true
}

// runningTarget proves the service has exactly one operational target and that it
// links, with EXACT certainty, to the revision the fixture deployed. "Some target
// exists" would pass with an unresolved link, which is the failure this fixture is
// built to make impossible.
func (p *prober) runningTarget(serviceKey, revisionKey string) string {
	if serviceKey == "" {
		return ""
	}
	targets := p.list("target", serviceKey)
	if len(targets) != 1 {
		p.failf("service %s has %d operational targets, want exactly 1 (%s)", serviceKey, len(targets), keysOf(targets))
		return ""
	}
	key := targets[0].Key
	d := p.detail("target", key)
	if d == nil || d.Target == nil {
		return ""
	}
	if d.Target.LinkState != "exact" {
		p.failf("target %s links to its revision as %q, want exact", key, d.Target.LinkState)
		return ""
	}
	switch {
	case d.Target.Revision == nil:
		p.failf("target %s carries no revision reference", key)
		return ""
	case revisionKey != "" && d.Target.Revision.Key != revisionKey:
		p.failf("target %s runs revision %s, want %s", key, d.Target.Revision.Key, revisionKey)
		return ""
	}
	return key
}

// reconciledEdge is the fixture's reason to exist: the SAME snapshot holds the
// declared dependency (from the OCI contract revision) and the observed call
// (from the managed observation source), and the backend — not this checker —
// decided they are the same relationship.
func (p *prober) reconciledEdge(fromService, toService string) {
	if fromService == "" || toService == "" {
		return
	}
	q := url.Values{
		"kind": {"service"}, "key": {fromService}, "perspective": {"service"},
		"direction": {"dependencies"}, "views": {"expected,observed,differences"},
	}
	var nb fleet.Neighborhood
	if err := p.get("/api/fleet/neighborhood", q, &nb); err != nil {
		p.failf("reading the %s neighborhood: %v", fromService, err)
		return
	}
	for _, e := range nb.Edges {
		if e.Relation != "dependency" || e.From.Key != fromService || e.To.Key != toService {
			continue
		}
		if !e.Expected {
			p.failf("the %s -> %s edge is not declared", fromService, toService)
		}
		if !e.Observed {
			p.failf("the %s -> %s edge is not observed", fromService, toService)
		}
		if e.Difference != "matched" {
			p.failf("the %s -> %s reconciliation is %q, want matched", fromService, toService, e.Difference)
		}
		if !hasSource(e.ObservationSources, p.want.ObservationSource) {
			p.failf("the %s -> %s observation is not attributed to %q", fromService, toService, p.want.ObservationSource)
		}
		return
	}
	p.failf("there is no %s -> %s dependency edge at all", fromService, toService)
}

// evidenceTarget proves the remote, signed evidence target survived the fixture's
// enrichment: the vertical must hold K8s, OCI, offline observation and the
// Evidence Server at once, not trade one for another.
func (p *prober) evidenceTarget(serviceKey string) string {
	if serviceKey == "" {
		return ""
	}
	for _, t := range p.list("target", serviceKey) {
		d := p.detail("target", t.Key)
		if d == nil || d.Target == nil {
			continue
		}
		if d.Target.Source == p.want.EvidenceSource || sliceHas(d.Target.Sources.Items, p.want.EvidenceSource) {
			return t.Key
		}
	}
	p.failf("service %s has no target attributed to %q", serviceKey, p.want.EvidenceSource)
	return ""
}

func hasSource(p fleet.ObservationSourcesPreview, id string) bool {
	for _, s := range p.Items {
		if s.Source == id {
			return true
		}
	}
	return false
}

func sliceHas(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}

func keysOf(refs []fleet.EntityRef) string {
	if len(refs) == 0 {
		return "none"
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Key
	}
	return strings.Join(out, ", ")
}

func versionsOf(refs []fleet.EntityRef) string {
	if len(refs) == 0 {
		return "none"
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Version
	}
	return strings.Join(out, ", ")
}
