// Package detect is the wire format of what the Pacto operator detects in a
// cluster: the registry most namespaces pull from, the cloud, a sign-in
// provider, an exposure controller, the Prometheus Operator and the node
// kernels. The operator writes one Result into its <fullname>-detected
// ConfigMap; the fleet, the dashboard and the CLI read it back as inferred
// provider rows. Everything here is a proposal: nothing in it turns a feature
// on, and it never makes an answer partial.
package detect

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

// SchemaVersion is the major version of the format this package writes. A
// Result of another major is refused; unknown fields of this one are ignored.
const SchemaVersion = "1"

// The ConfigMap the operator writes: the key that holds the Result, the field
// manager it applies with and the label it sets. A reader trusts a ConfigMap
// only when that manager owns its data and the label is set.
const (
	ConfigMapKey   = "detected.json"
	FieldManager   = "pacto-detection"
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedBy      = "pacto-operator"
)

// StaleAfter is how old a Result may be before its rows read stale: three
// detection intervals.
const StaleAfter = 15 * time.Minute

// MaxFutureSkew is how far ahead of the reader's clock a Result's AsOf may be
// before the reader refuses it as invalid.
const MaxFutureSkew = 2 * time.Minute

// Completeness of one entry.
const (
	Complete = "complete"
	Partial  = "partial"
	Unknown  = "unknown"
)

// Bounds a hand-edited ConfigMap cannot exceed once decoded.
const (
	MaxEntries = 16
	MaxDetails = 5
	MaxRunes   = 256
	// MaxNamed caps the namespaces and the objects of one entry.
	MaxNamed = 50
)

// Result is one detection run.
type Result struct {
	SchemaVersion string    `json:"schemaVersion"`
	AsOf          time.Time `json:"asOf"`
	// Cluster is the id of the cluster the operator read.
	Cluster string `json:"cluster,omitempty"`
	// ClusterName is the cluster's display name, so a row read with no
	// Kubernetes source to name the cluster still reads as a name.
	ClusterName string `json:"clusterName,omitempty"`
	// Namespaces is the watched scope of a namespace-list install, empty when
	// the operator reads every namespace.
	Namespaces []string `json:"namespaces,omitempty"`
	Entries    []Entry  `json:"entries"`
}

// Entry is one thing the operator detected. Kind is an open string:
// registry, cloud, sign-in, exposure, metrics and nodes today.
type Entry struct {
	Kind         string `json:"kind"`
	Value        string `json:"value,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
	Source       string `json:"source,omitempty"`
	Completeness string `json:"completeness"`
	Reason       string `json:"reason,omitempty"`
	// Hint is a command for a person to review; nothing runs it.
	Hint    string   `json:"hint,omitempty"`
	Details []string `json:"details,omitempty"`
	// Namespaces are the namespaces the entry's facts come from.
	Namespaces []string `json:"namespaces,omitempty"`
	// Objects are the namespaced objects the entry names, such as a Gateway.
	Objects []Object `json:"objects,omitempty"`
	// Scope is fleet.ScopeCluster for an entry built from a cluster-wide read;
	// an entry with no scope keeps its facts in its namespaces and objects.
	Scope string `json:"scope,omitempty"`
}

// Object is one object an entry names.
type Object struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Decode reads a Result. Unknown fields are ignored, another major version is
// refused, and every string is cleaned of control and bidi characters and
// truncated, every list capped, so a hand-edited ConfigMap cannot flood an
// answer or a terminal.
func Decode(data []byte) (Result, error) {
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{}, fmt.Errorf("decoding detection: %w", err)
	}
	if major, _, _ := strings.Cut(res.SchemaVersion, "."); major != SchemaVersion {
		return Result{}, fmt.Errorf("detection schema version %q is not %s", clean(res.SchemaVersion), SchemaVersion)
	}
	sanitize(&res)
	return res, nil
}

// Encode writes res as the operator stores it: entries sorted by kind and
// every string cleaned the way [Decode] cleans it. A list longer than its
// bound is an error, so the operator never writes what a reader would cut.
func Encode(res Result) ([]byte, error) {
	if len(res.Entries) > MaxEntries {
		return nil, fmt.Errorf("detection has %d entries, more than %d", len(res.Entries), MaxEntries)
	}
	for _, e := range res.Entries {
		if len(e.Details) > MaxDetails || len(e.Namespaces) > MaxNamed || len(e.Objects) > MaxNamed {
			return nil, fmt.Errorf("detection entry %q names more than its bounds allow", e.Kind)
		}
	}
	res.SchemaVersion = SchemaVersion
	res.Entries = slices.Clone(res.Entries)
	slices.SortStableFunc(res.Entries, func(a, b Entry) int { return strings.Compare(a.Kind, b.Kind) })
	sanitize(&res)
	return json.Marshal(res)
}

// UnknownEntry is an entry of kind that could not be read, and why.
func UnknownEntry(kind, reason string) Entry {
	return Entry{Kind: kind, Completeness: Unknown, Reason: reason}
}

// sanitize cleans every string of res and caps every list.
func sanitize(res *Result) {
	res.Cluster, res.ClusterName = clean(res.Cluster), clean(res.ClusterName)
	res.Namespaces = cleanAll(res.Namespaces, MaxNamed)
	res.Entries = capped(res.Entries, MaxEntries)
	for i := range res.Entries {
		e := &res.Entries[i]
		for _, s := range []*string{&e.Kind, &e.Value, &e.Evidence, &e.Source, &e.Completeness, &e.Reason, &e.Hint, &e.Scope} {
			*s = clean(*s)
		}
		e.Details = cleanAll(e.Details, MaxDetails)
		e.Namespaces = cleanAll(e.Namespaces, MaxNamed)
		e.Objects = capped(e.Objects, MaxNamed)
		for j := range e.Objects {
			o := &e.Objects[j]
			o.Kind, o.Namespace, o.Name = clean(o.Kind), clean(o.Namespace), clean(o.Name)
		}
	}
}

// capped is s with at most n elements.
func capped[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// cleanAll is the first n of ss, each cleaned.
func cleanAll(ss []string, n int) []string {
	ss = capped(ss, n)
	for i := range ss {
		ss[i] = clean(ss[i])
	}
	return ss
}

// clean drops C0 and C1 control characters and the Unicode bidi controls from
// s, then keeps its first MaxRunes runes.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (r >= 0x200E && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > MaxRunes {
		return string(r[:MaxRunes])
	}
	return s
}
