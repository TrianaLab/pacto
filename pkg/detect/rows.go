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

// rowNames are the row names of the kinds Pacto detects today; another kind
// is title-cased.
var rowNames = map[string]string{
	"registry": "Registry", "cloud": "Cloud", "sign-in": "Sign-in",
	"exposure": "Exposure", "metrics": "Metrics", "nodes": "Nodes",
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
}

// ProviderRows maps every entry of res to an inferred provider row of source
// at now: complete is available, partial is partial, unknown is unavailable,
// and a row older than [StaleAfter] is stale. A reason becomes one
// DETECTION_<KIND>_UNKNOWN or _PARTIAL limitation, and every namespace and
// object the entry names becomes a row object, so a restricted view narrows
// it. Every surface (WASM demo, Compose demo, dashboard, CLI, fleet source)
// maps a Result through this one function.
func ProviderRows(res Result, source string, now time.Time) []fleet.Provider {
	rows := make([]fleet.Provider, 0, len(res.Entries))
	for _, e := range res.Entries {
		asOf := res.AsOf
		p := fleet.Provider{
			ID: e.Kind, Name: rowName(e.Kind), Evidence: evidence(e, source, res.Cluster),
			UsedFor: []string{cmp.Or(usedFor[e.Kind], "proposal; nothing is turned on")}, Status: status(e.Completeness, now.Sub(asOf)),
			Provenance: fleet.ProvenanceInferred, AsOf: &asOf, Hint: e.Hint, Details: slices.Clone(e.Details),
			Scope: e.Scope, Cluster: res.Cluster, ClusterID: res.Cluster,
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
// came from.
func evidence(e Entry, source, cluster string) string {
	from := "from " + cmp.Or(e.Source, source)
	if cluster != "" {
		from += ", cluster " + cluster
	}
	return strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{e.Value, e.Evidence}, func(s string) bool { return s == "" }), " ") + " (" + from + ")")
}

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
