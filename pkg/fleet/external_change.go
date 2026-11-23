package fleet

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ExternalOpenAPIMetadataKey is the metadata key under which an inferred
// contract records the vendor OpenAPI document its catalog entry links.
// metadata is free-form in every contract version, so a released reader
// reads it.
const ExternalOpenAPIMetadataKey = "run.pacto.external.openapi"

// ExternalEngineMetadataKey is the metadata key under which an inferred
// contract records the observed engines of the managed resources of its
// external system (see [ExternalEngineOf]).
const ExternalEngineMetadataKey = "run.pacto.external.engine"

// AttentionExternalChanged is the code of the item raised while a checked
// change of an external system's inferred contract is fresh.
const AttentionExternalChanged = "EXTERNAL_CHANGED"

// ExternalClient is the client library, and its major, a consumer revision's
// inferred record names for an external system.
type ExternalClient struct {
	SDK        string      `json:"sdk"`
	Major      int         `json:"major"`
	Confidence string      `json:"confidence" enum:"possible" doc:"A client library in a manifest proves no call, so a client major is always possible"`
	Revision   RevisionKey `json:"revision" doc:"The consumer revision whose inferred record names it"`
}

// ExternalClientOf is the client the inferred record of revision rk names
// for key, nil when rk has no record or the record names no client major for
// key: a missing major is absent, never guessed.
func ExternalClientOf(snap *FleetSnapshot, rk RevisionKey, key ExternalKey) *ExternalClient {
	rev := snap.Revisions[rk]
	if rk == "" || rev == nil || rev.Record == nil {
		return nil
	}
	for _, f := range rev.Record.Fields {
		if x := f.External; x != nil && x.Identity == string(key) && x.Client != nil && x.Client.Major != nil {
			return &ExternalClient{SDK: x.Client.SDK, Major: *x.Client.Major, Confidence: "possible", Revision: rk}
		}
	}
	return nil
}

// ExternalProviderChange is what changed between the two revisions of an
// inferred contract an ExternalProvider shows first.
type ExternalProviderChange struct {
	From        string   `json:"from" doc:"The version of the older revision"`
	To          string   `json:"to" doc:"The version of the newer revision"`
	At          string   `json:"at,omitempty" doc:"When the newer revision's surface was computed"`
	Checked     bool     `json:"checked" doc:"True only when this installation's operator signed both revisions; an unchecked change raises no attention"`
	Added       []string `json:"added,omitempty" doc:"Surface values as <list> <value>, for example clients stripe-go/77"`
	Removed     []string `json:"removed,omitempty" doc:"Surface values as <list> <value>, for example clients stripe-go/76"`
	OpenAPIFrom string   `json:"openapiFrom,omitempty" doc:"The vendor OpenAPI document the older revision links, set when the digest moved"`
	OpenAPITo   string   `json:"openapiTo,omitempty" doc:"The vendor OpenAPI document the newer revision links, set when the digest moved"`
	EngineFrom  string   `json:"engineFrom,omitempty" doc:"The observed engines the older revision records, set when the observed engine set moved"`
	EngineTo    string   `json:"engineTo,omitempty" doc:"The observed engines the newer revision records, set when the observed engine set moved"`
}

// ExternalEngineOf is the observed engines of rec's managed resources: the
// sorted, unique engine strings joined with ", ", "" when none reports one.
// A desired-only engine (EngineRequested) is never part of it.
func ExternalEngineOf(rec *ExternalRecord) string {
	if rec == nil {
		return ""
	}
	var engines []string
	for _, r := range rec.Resources {
		if r.Engine != "" {
			engines = append(engines, r.Engine)
		}
	}
	slices.Sort(engines)
	return strings.Join(slices.Compact(engines), ", ")
}

// ExternalOpenAPIDigest is the digest a vendor OpenAPI link is pinned with:
// its text after the last "@", "" for no link.
func ExternalOpenAPIDigest(link string) string {
	return link[strings.LastIndexByte(link, '@')+1:]
}

// openAPILink is the vendor OpenAPI document rev records, or "".
func openAPILink(rev *ContractRevision) string {
	return metadataString(rev, ExternalOpenAPIMetadataKey)
}

// revisionEngine is the observed engines rev records, or "".
func revisionEngine(rev *ContractRevision) string {
	return metadataString(rev, ExternalEngineMetadataKey)
}

// metadataString is rev's string metadata value under key, or "".
func metadataString(rev *ContractRevision, key string) string {
	v, _ := rev.Contract.Metadata[key].(string)
	return v
}

// providerChange is what changed from older to newer: the surface values
// added and removed, the OpenAPI link, compared by digest (a document
// re-hosted under the same digest is no change), and the observed engines.
// It is nil when either is
// nil or nothing differs.
func providerChange(newer, older *ContractRevision) *ExternalProviderChange {
	if newer == nil || older == nil {
		return nil
	}
	c := &ExternalProviderChange{From: older.Version, To: newer.Version, Checked: operatorSigned(newer) && operatorSigned(older)}
	ns, os := surfaceOf(newer), surfaceOf(older)
	c.At = ns.AsOf
	for _, l := range []struct {
		name     string
		new, old []contract.SurfaceValue
	}{{"hosts", ns.Hosts, os.Hosts}, {"ports", ns.Ports, os.Ports}, {"clients", ns.Clients, os.Clients}} {
		c.Added = append(c.Added, missing(l.name, l.new, l.old)...)
		c.Removed = append(c.Removed, missing(l.name, l.old, l.new)...)
	}
	if from, to := openAPILink(older), openAPILink(newer); ExternalOpenAPIDigest(from) != ExternalOpenAPIDigest(to) {
		c.OpenAPIFrom, c.OpenAPITo = from, to
	}
	if from, to := revisionEngine(older), revisionEngine(newer); from != to {
		c.EngineFrom, c.EngineTo = from, to
	}
	if len(c.Added) == 0 && len(c.Removed) == 0 && c.OpenAPIFrom == "" && c.OpenAPITo == "" && c.EngineFrom == "" && c.EngineTo == "" {
		return nil
	}
	return c
}

// surfaceOf is rev's recorded surface, empty when it records none.
func surfaceOf(rev *ContractRevision) contract.InferredSurface {
	if rev.Record == nil || rev.Record.Surface == nil {
		return contract.InferredSurface{}
	}
	return *rev.Record.Surface
}

// missing is each value of a absent from b, as "<list> <value>", sorted.
func missing(list string, a, b []contract.SurfaceValue) []string {
	var out []string
	for _, v := range a {
		if !slices.ContainsFunc(b, func(o contract.SurfaceValue) bool { return o.Value == v.Value }) {
			out = append(out, list+" "+v.Value)
		}
	}
	slices.Sort(out)
	return out
}

// externalChangedAttention reports one EXTERNAL_CHANGED item per external
// system whose inferred contract changed, checked, within
// MaxExternalChangeAge: its linked OpenAPI digest, its observed engine or its
// clients moved. Host
// and port moves raise nothing: they are the organization's own calls. An
// external item names no service, so a page or focus filters it out.
func (q *Query) externalChangedAttention() []AttentionItem {
	var out []AttentionItem
	for _, k := range slices.Sorted(maps.Keys(q.snap.Externals)) {
		if it, ok := q.externalChangedItem(q.snap.Externals[k]); ok {
			out = append(out, it)
		}
	}
	return out
}

// externalChangedItem is rec's EXTERNAL_CHANGED item, when it raises one.
func (q *Query) externalChangedItem(rec *ExternalRecord) (AttentionItem, bool) {
	if rec.Provider == nil || rec.Provider.Change == nil || !rec.Provider.Change.Checked {
		return AttentionItem{}, false
	}
	c := rec.Provider.Change
	at, err := time.Parse(time.RFC3339, c.At)
	if err != nil || q.snap.GeneratedAt.Sub(at) > MaxExternalChangeAge {
		return AttentionItem{}, false
	}
	id := string(rec.Key)
	it := AttentionItem{
		Entity: externalEntityRef(rec), Severity: severityInfo, Code: AttentionExternalChanged, Category: categoryKnowledge,
		Summary: "External system changed", NextStep: "pacto impact --changed " + ExternalSubjectPrefix + id,
	}
	between := " between " + c.From + " and " + c.To
	consumers := consumersToReview(rec)
	switch {
	case c.OpenAPIFrom != "" && c.OpenAPITo != "":
		it.Severity = severityWarning
		it.Label = id + "'s linked OpenAPI document moved: " + consumers
		it.Reason = "the organization catalog's linked OpenAPI digest for " + id + " moved from " +
			ExternalOpenAPIDigest(c.OpenAPIFrom) + " to " + ExternalOpenAPIDigest(c.OpenAPITo) + between
	case c.OpenAPITo != "":
		it.Label = "the organization catalog now links " + id + "'s OpenAPI document"
		it.Reason = "the inferred contract of " + id + " links " + c.OpenAPITo + between
	case c.OpenAPIFrom != "":
		it.Label = "the organization catalog no longer links " + id + "'s OpenAPI document"
		it.Reason = "the inferred contract of " + id + " no longer links " + c.OpenAPIFrom + between
	case c.EngineFrom != "" && c.EngineTo != "":
		// A restricted view never gets here: withoutSurface drops the values.
		it.Severity = severityWarning
		it.Label = id + " engine " + c.EngineFrom + " -> " + c.EngineTo + ": " + consumers
		it.Reason = "the observed engine of " + id + " moved from " + c.EngineFrom + " to " + c.EngineTo + between
	default:
		return clientsChangedItem(it, id, c, rec.Provider.SurfaceHidden)
	}
	return it, true
}

// consumersToReview is "<n> consumers to review" for rec, hidden consumers
// included.
func consumersToReview(rec *ExternalRecord) string {
	if n := rec.ConsumersTotal + rec.HiddenConsumers; n != 1 {
		return strconv.Itoa(n) + " consumers to review"
	}
	return "1 consumer to review"
}

// clientsChangedItem is it worded for a move of id's clients, when c moved
// any. A restricted view raises nothing: the move may be a hidden
// consumer's, and its values are dropped.
func clientsChangedItem(it AttentionItem, id string, c *ExternalProviderChange, hidden bool) (AttentionItem, bool) {
	added, removed := clientValues(c.Added), clientValues(c.Removed)
	if hidden || len(added)+len(removed) == 0 {
		return AttentionItem{}, false
	}
	if len(added) > 0 {
		it.Label = "a " + id + " client moved to " + strings.Join(added, ", ")
	} else {
		it.Label = "a " + id + " client is no longer used: " + strings.Join(removed, ", ")
	}
	it.Reason = "the clients of " + id + "'s inferred contract changed between " + c.From + " and " + c.To
	return it, true
}

// clientValues is the clients values among change values vs.
func clientValues(vs []string) []string {
	var out []string
	for _, v := range vs {
		if c, ok := strings.CutPrefix(v, "clients "); ok {
			out = append(out, c)
		}
	}
	return out
}
