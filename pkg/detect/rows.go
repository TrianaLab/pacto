package detect

import (
	"cmp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// rowNames are the row names of every kind the operator's detector can emit
// (TestDetectEntriesFitMaxEntries holds them to [MaxEntries]); another kind
// is title-cased.
var rowNames = map[string]string{
	"registry": "Registry", "cloud": "Cloud", "sign-in": "Sign-in",
	"exposure": "Exposure", "metrics": "Metrics", "nodes": "Nodes",
	"pull-secret": "Pull Secret", "otel-collector": "OTel Collector", "tempo": "Tempo",
	"jaeger": "Jaeger", "datadog": "Datadog", "traces": "Traces",
	"istio-egress": "Istio egress", "cilium-fqdn": "Cilium FQDN", "egress-gateway": "Egress gateway",
	"crossplane": "Crossplane", "evidence": "Evidence", "recorder": "Recorder", fleet.ProviderApplier: "Applier",
	"backstage": "Backstage catalog",
}

// usedFor says, per kind, what a person may do with the proposal. Nothing is
// turned on by a detected row.
var usedFor = map[string]string{
	"registry": "proposal for delivery; set it yourself",
	"cloud":    "proposal for registry credentials; set them yourself",
	"sign-in":  "proposal for dashboard sign-in; set it yourself",
	"exposure": "proposal for the dashboard route; set it yourself",
	"metrics":  "read passively; nothing is turned on",
	"nodes":    "proposal for runtime conformance; nothing is turned on",
	"pull-secret": "proposal for registryPullSecret: it needs push on <registry>/pacto-*; " +
		"a pod pull Secret usually grants pull only",
	"otel-collector":      observedEdges,
	"tempo":               observedEdges,
	"jaeger":              observedEdges,
	"datadog":             observedEdges,
	"traces":              observedEdges,
	"istio-egress":        egressSentence,
	"cilium-fqdn":         egressSentence,
	"egress-gateway":      egressSentence,
	fleet.ProviderApplier: "proposal for delivery in profile idp; set it on the Pacto release yourself",
	"crossplane":          "provisioning engine; profile idp offers its XRDs once accepted",
	"evidence":            "Evidence Server for provisioning reports (profile idp)",
	"recorder":            "signed delivery records (profile idp)",
	"backstage":           "inferred owners, domains and dependencies, read by the dashboard; nothing is turned on",
}

// egressSentence is what an egress reader's row is used for.
const egressSentence = "reads calls leaving the cluster; nothing is turned on"

// EgressKinds are the kinds whose rows read calls leaving the cluster: their
// UsedFor carries [fleet.UseExternalEgress] after the sentence.
var EgressKinds = []string{"istio-egress", "cilium-fqdn", "egress-gateway"}

// observedEdges is what a trace-backend row is used for.
const observedEdges = "observed edges, read by the dashboard; nothing is turned on"

// ProviderRows maps every entry of res to an inferred provider row of source
// at now: complete is available, partial is partial, unknown is unavailable,
// and a row older than [StaleAfter] is stale. A reason becomes one
// DETECTION_<KIND>_UNKNOWN or _PARTIAL limitation, and every namespace and
// object the entry names becomes a row object, so a restricted view narrows
// it. Every surface (WASM demo, Compose demo, dashboard, CLI, fleet source)
// maps a Result through this one function. An Installation block adds the
// one declared row; every row built from the entries stays inferred. An
// Applier row's hint is always [FluxInstallHint]: the ConfigMap decides
// whether the offer shows, never what a person copies.
func ProviderRows(res Result, source string, now time.Time) []fleet.Provider {
	rows := make([]fleet.Provider, 0, len(res.Entries)+1)
	if res.Installation != nil {
		rows = append(rows, installationRow(res, source, now))
	}
	for _, e := range res.Entries {
		asOf, cluster := res.AsOf, cmp.Or(res.ClusterName, res.Cluster)
		p := fleet.Provider{
			ID: e.Kind, Name: rowName(e.Kind), Evidence: evidence(e, source, cluster),
			UsedFor: rowUsedFor(e.Kind), Status: status(e.Completeness, now.Sub(asOf)),
			Provenance: fleet.ProvenanceInferred, AsOf: &asOf, Hint: e.Hint, Details: slices.Clone(e.Details),
			Scope: e.Scope, Cluster: cluster, ClusterID: res.Cluster,
		}
		if e.Kind == fleet.ProviderApplier && p.Hint != "" {
			p.Hint = FluxInstallHint
		}
		for _, ns := range e.Namespaces {
			p.Objects = append(p.Objects, fleet.ProviderObject{Kind: "Namespace", Namespace: ns, Name: ns})
		}
		for _, o := range e.Objects {
			p.Objects = append(p.Objects, fleet.ProviderObject(o))
		}
		if e.Reason != "" && (e.Completeness == Partial || e.Completeness == Unknown) {
			code := "DETECTION_" + strings.ToUpper(strings.ReplaceAll(e.Kind, "-", "_")) + "_" + strings.ToUpper(e.Completeness)
			p.Limitations = []fleet.Limitation{{Code: code, Source: source, Message: e.Reason}}
		}
		rows = append(rows, p)
	}
	return rows
}

// rowUsedFor is the UsedFor of a row of kind: its sentence, then
// [fleet.UseExternalEgress] for an egress kind.
func rowUsedFor(kind string) []string {
	out := []string{cmp.Or(usedFor[kind], "proposal; nothing is turned on")}
	if slices.Contains(EgressKinds, kind) {
		out = append(out, fleet.UseExternalEgress)
	}
	return out
}

// rowName is the row name of kind.
func rowName(kind string) string {
	if n, ok := rowNames[kind]; ok {
		return n
	}
	if kind == "" {
		return "Detected"
	}
	r, n := utf8.DecodeRuneInString(kind)
	return string(unicode.ToUpper(r)) + kind[n:]
}

// evidence is what the row says: the value, the entry's evidence and where it
// came from. A bare state (the Evidence and Recorder rows' on and off) ends
// with a colon before its reason, so the two never read as one sentence.
func evidence(e Entry, source, cluster string) string {
	from := "from " + cmp.Or(e.Source, source)
	if cluster != "" {
		from += ", cluster " + cluster
	}
	sep := " "
	if stateKinds[e.Kind] {
		sep = ": "
	}
	return strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{e.Value, e.Evidence}, func(s string) bool { return s == "" }), sep) + " (" + from + ")")
}

// stateKinds are the kinds whose Value is a bare state (on, off or unknown).
var stateKinds = map[string]bool{"evidence": true, "recorder": true}

// status is the row status of an entry of completeness c that is age old.
// Unknown stays unavailable however old it is; anything else read longer ago
// than StaleAfter is stale.
func status(c string, age time.Duration) fleet.SourceStatus {
	switch {
	case c != Complete && c != Partial:
		return fleet.SourceUnavailable
	case age > StaleAfter:
		return fleet.SourceStale
	case c == Partial:
		return fleet.SourcePartial
	}
	return fleet.SourceAvailable
}
