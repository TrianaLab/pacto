package detect

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
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
	"backstage": "Backstage catalog", inventoryKind: "Cluster inventory", fleet.ProviderManagedResources: "Managed resources",
	"terraform": "Terraform state", "cloud-inventory": "Cloud inventory",
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
	"managed-resources": managedSentence, "terraform": managedSentence, "cloud-inventory": managedSentence,
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
	"backstage":           "inferred owners, domains, dependencies and golden paths, read by the dashboard; nothing is turned on",
	inventoryKind:         "candidate clusters for pacto cluster install; nothing joins on its own",
}

// managedSentence is what a managed-resource reader's row is used for.
const managedSentence = "reads managed cloud resources and the services that use them; nothing is turned on"

// ManagedKinds are the kinds whose rows read managed cloud resources: their
// UsedFor carries [fleet.UseManagedResources] after the sentence.
var ManagedKinds = []string{fleet.ProviderManagedResources, "terraform", "cloud-inventory"}

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
// whether the offer shows, never what a person copies. A cluster-inventory
// row's hint is rebuilt by inventoryHint from its namespaces.
func ProviderRows(res Result, source string, now time.Time) []fleet.Provider {
	rows := make([]fleet.Provider, 0, len(res.Entries)+1)
	if res.Installation != nil {
		rows = append(rows, installationRow(res, source, now))
	}
	for _, e := range res.Entries {
		e, capped := managedCap(res, e)
		asOf, cluster := res.AsOf, cmp.Or(res.ClusterName, res.Cluster)
		p := fleet.Provider{
			ID: e.Kind, Name: rowName(e.Kind), Evidence: evidence(e, source, cluster),
			UsedFor: rowUsedFor(e), Status: status(e.Completeness, now.Sub(asOf)),
			Provenance: fleet.ProvenanceInferred, AsOf: &asOf, Hint: e.Hint, Details: slices.Clone(e.Details),
			Scope: e.Scope, Cluster: cluster, ClusterID: res.Cluster,
		}
		switch {
		case p.Hint == "":
		case e.Kind == fleet.ProviderApplier:
			p.Hint = FluxInstallHint
		case e.Kind == inventoryKind:
			p.Hint = inventoryHint(e.Namespaces)
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
		if capped {
			p.Limitations = append(p.Limitations, CappedLimitations(res, source)...)
		}
		rows = append(rows, p)
	}
	return rows
}

// backstageNotDetectedUse is what a Backstage row that found no catalog is
// used for: nothing is read until a URL is set.
const backstageNotDetectedUse = "set dashboard.backstage.url to read its owners and golden paths; nothing is read"

// rowUsedFor is the UsedFor of entry e's row: its kind's sentence, then
// [fleet.UseExternalEgress] for an egress kind or
// [fleet.UseManagedResources] for a managed kind. A Backstage catalog that
// was not detected reads nothing, so its row says how to read one.
func rowUsedFor(e Entry) []string {
	kind := e.Kind
	if kind == "backstage" && e.Value == NotDetected {
		return []string{backstageNotDetectedUse}
	}
	out := []string{cmp.Or(usedFor[kind], "proposal; nothing is turned on")}
	switch {
	case slices.Contains(EgressKinds, kind):
		out = append(out, fleet.UseExternalEgress)
	case slices.Contains(ManagedKinds, kind):
		out = append(out, fleet.UseManagedResources)
	}
	return out
}

// managedCap lowers a complete managed entry of a capped res to partial and
// says whether it did: what lies past the cap is unknown. Capped Secret refs
// lower the managed-resources entry, whose consumers they join.
func managedCap(res Result, e Entry) (Entry, bool) {
	refs := res.SecretRefsCapped() && e.Kind == fleet.ProviderManagedResources
	if !res.ManagedCapped() && !refs || !slices.Contains(ManagedKinds, e.Kind) || e.Completeness != Complete {
		return e, false
	}
	e.Completeness = Partial
	return e, true
}

// CappedLimitations are the limitations of res's capped lists read by
// source: MANAGED_RESOURCES_CAPPED and SECRET_REFS_CAPPED.
func CappedLimitations(res Result, source string) []fleet.Limitation {
	var out []fleet.Limitation
	if res.ManagedCapped() {
		out = append(out, fleet.Limitation{Code: fleet.LimitationManagedResourcesCapped, Source: source,
			Message: strconv.Itoa(res.ManagedResourcesTotal-len(res.ManagedResources)) + " of " + strconv.Itoa(res.ManagedResourcesTotal) +
				" managed resources past the bound of " + strconv.Itoa(MaxManagedResources) + " were not kept; raise MaxManagedResources if a real cluster hits it"})
	}
	if res.SecretRefsCapped() {
		out = append(out, fleet.Limitation{Code: fleet.LimitationSecretRefsCapped, Source: source,
			Message: "the Secret names the running pods reference were not all kept or read, so a service that uses a managed resource through its connection Secret may be missing"})
	}
	return out
}

// ReadsPartial is one SOURCE_PARTIAL limitation of source per managed-kind
// entry of res that did not read everything: partial, or unknown with no
// hint (a read that failed; an entry with a hint is off until a person turns
// it on).
func ReadsPartial(res Result, source string) []fleet.Limitation {
	var out []fleet.Limitation
	for _, e := range res.Entries {
		if slices.Contains(ManagedKinds, e.Kind) && (e.Completeness == Partial || e.Completeness == Unknown && e.Hint == "") {
			out = append(out, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: source, Message: e.Kind + " did not read everything: " + cmp.Or(e.Reason, e.Completeness)})
		}
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

// inventoryHint is the one proposal a cluster-inventory row carries, rebuilt
// by every reader from the namespaces that are DNS labels, so a hand-edited
// hint never reaches a person. Like [FluxInstallHint] it names values of the
// Pacto release the person already runs, at the chart version it runs.
func inventoryHint(namespaces []string) string {
	hint := "Set clusterInventory on this Pacto release, at the chart version it runs: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator" +
		" --version <version> -n <namespace> --reset-then-reuse-values --set clusterInventory.enabled=true"
	ns := slices.DeleteFunc(slices.Clone(namespaces), func(n string) bool { return !dnsLabel.MatchString(n) })
	slices.Sort(ns)
	if ns = slices.Compact(ns); len(ns) > 0 {
		hint += " --set 'clusterInventory.secretNamespaces={" + strings.Join(ns, ",") + "}'"
	}
	return hint
}

// dnsLabel is a namespace name: a DNS label of at most 63 characters.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
