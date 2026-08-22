// Package otelobserver derives observed service dependencies from OpenTelemetry
// trace data and expresses them as Pacto evidence. It reads the OTLP/JSON trace
// encoding (no OTel SDK dependency), extracts caller→callee edges from outbound
// (CLIENT/PRODUCER) spans, and emits one [evidence.EvidenceSet] per caller with
// a DependencyReachable observation for each callee it actually reached at
// runtime. That evidence is what makes "declared vs observed" reconciliation
// possible: the graph can compare what a contract declares against what traffic
// proves. The observer only reports what it saw; it never asserts a dependency
// is missing (absence of a trace is not evidence of absence).
package otelobserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// OTLP SpanKind values that denote an outbound call (the span's peer is a
// dependency of the resource's service).
const (
	spanKindClient   = 3
	spanKindProducer = 4
)

// calleeKeys is the attribute precedence for identifying a span's callee, from
// most explicit (an intentional peer service name) to least. The first present
// non-empty value wins.
var calleeKeys = []string{
	"peer.service",
	"rpc.service",
	"server.address",
	"net.peer.name",
	"db.system",
	"messaging.system",
}

// TracesData is the subset of the OTLP/JSON trace payload the observer reads.
type TracesData struct {
	ResourceSpans []ResourceSpans `json:"resourceSpans"`
}

// ResourceSpans groups spans emitted by one resource (one service instance).
type ResourceSpans struct {
	Resource   Resource     `json:"resource"`
	ScopeSpans []ScopeSpans `json:"scopeSpans"`
}

// Resource carries the resource attributes (notably service.name).
type Resource struct {
	Attributes []KeyValue `json:"attributes"`
}

// ScopeSpans groups spans by instrumentation scope.
type ScopeSpans struct {
	Spans []Span `json:"spans"`
}

// Span is one recorded span; kind, attributes and the start/end timestamps are
// read. The OTLP/JSON timestamps are unsigned-nanosecond strings.
type Span struct {
	Name              string     `json:"name"`
	Kind              spanKind   `json:"kind"`
	StartTimeUnixNano string     `json:"startTimeUnixNano"`
	EndTimeUnixNano   string     `json:"endTimeUnixNano"`
	Attributes        []KeyValue `json:"attributes"`
}

// KeyValue is an OTLP attribute.
type KeyValue struct {
	Key   string   `json:"key"`
	Value AnyValue `json:"value"`
}

// AnyValue reads only the string form of an OTLP attribute value; non-string
// values (service identifiers are always strings) decode to "" and are ignored.
type AnyValue struct {
	StringValue string `json:"stringValue"`
}

// spanKind accepts the OTLP/JSON kind field as either an integer (3) or the
// proto3 JSON string enum ("SPAN_KIND_CLIENT"), since exporters emit both.
type spanKind int

func (k *spanKind) UnmarshalJSON(b []byte) error {
	if n, err := strconv.Atoi(string(b)); err == nil {
		*k = spanKind(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "SPAN_KIND_CLIENT":
		*k = spanKindClient
	case "SPAN_KIND_PRODUCER":
		*k = spanKindProducer
	default:
		*k = 0 // unspecified/other kinds are not outbound calls
	}
	return nil
}

// Edge is an observed dependency: From (a service) reached To (its callee) in
// Count outbound spans, first witnessed at FirstSeen and last at LastSeen (the
// observation window derived from the spans' timestamps; zero when the traces
// carry none).
type Edge struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Count     int       `json:"count"`
	FirstSeen time.Time `json:"firstSeen,omitzero"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
}

// ParseTraces decodes an OTLP/JSON trace payload.
func ParseTraces(data []byte) (*TracesData, error) {
	var td TracesData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil, fmt.Errorf("decoding otlp traces: %w", err)
	}
	return &td, nil
}

// DependencyEdges derives deduplicated caller→callee edges from outbound spans,
// sorted by From then To for deterministic output. Self-edges and spans without
// a resolvable caller or callee are skipped.
func DependencyEdges(td *TracesData) []Edge {
	edges := map[[2]string]*edgeAgg{}
	for _, rs := range td.ResourceSpans {
		caller := attrValue(rs.Resource.Attributes, "service.name")
		if caller == "" {
			continue
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				foldSpanEdge(edges, caller, sp)
			}
		}
	}
	out := make([]Edge, 0, len(edges))
	for k, a := range edges {
		out = append(out, Edge{From: k[0], To: k[1], Count: a.count, FirstSeen: a.first, LastSeen: a.last})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

type edgeAgg struct {
	count       int
	first, last time.Time
}

// foldSpanEdge folds one outbound span into the caller->callee aggregate,
// widening the observation window from the span's start/end timestamps.
func foldSpanEdge(edges map[[2]string]*edgeAgg, caller string, sp Span) {
	if sp.Kind != spanKindClient && sp.Kind != spanKindProducer {
		return
	}
	callee := calleeName(sp.Attributes)
	if callee == "" || callee == caller {
		return
	}
	k := [2]string{caller, callee}
	a := edges[k]
	if a == nil {
		a = &edgeAgg{}
		edges[k] = a
	}
	a.count++
	if st := parseNano(sp.StartTimeUnixNano); !st.IsZero() && (a.first.IsZero() || st.Before(a.first)) {
		a.first = st
	}
	if et := parseNano(sp.EndTimeUnixNano); !et.IsZero() && et.After(a.last) {
		a.last = et
	}
}

// parseNano parses an OTLP unsigned-nanosecond timestamp string into a UTC time,
// returning the zero time for an empty, unparseable or out-of-int64-range value.
func parseNano(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > 1<<63-1 {
		return time.Time{}
	}
	return time.Unix(0, int64(n)).UTC()
}

// Options configure evidence emission.
type Options struct {
	// Collector names the evidence producer (provenance); defaults to "otel".
	Collector string
	// ObservedAt is the observation window end stamped on every EvidenceSet and
	// observation. Required.
	ObservedAt time.Time
	// ContractRef, if set, maps a caller service name to its immutable contract
	// ref so the ingestion host can evaluate the evidence. Unmapped services get
	// an empty ref.
	ContractRef func(service string) string
}

// EvidenceSets groups edges by caller and returns one EvidenceSet per caller,
// each carrying a DependencyReachable observation for every callee. The sets are
// ordered by subject service for deterministic output.
func EvidenceSets(edges []Edge, opts Options) []evidence.EvidenceSet {
	collector := opts.Collector
	if collector == "" {
		collector = "otel"
	}
	prov := evidence.Provenance{Collector: collector, DetectedAt: opts.ObservedAt}

	byCaller := map[string][]Edge{}
	order := []string{}
	for _, e := range edges {
		if _, seen := byCaller[e.From]; !seen {
			order = append(order, e.From)
		}
		byCaller[e.From] = append(byCaller[e.From], e)
	}
	sort.Strings(order)

	sets := make([]evidence.EvidenceSet, 0, len(order))
	for _, caller := range order {
		ref := ""
		if opts.ContractRef != nil {
			ref = opts.ContractRef(caller)
		}
		obs := make([]evidence.Observation, 0, len(byCaller[caller]))
		for _, e := range byCaller[caller] {
			obs = append(obs, evidence.NewDependencyReachable(
				evidence.SubjectRef{Kind: "dependency", Name: e.To}, true, prov))
		}
		sets = append(sets, evidence.EvidenceSet{
			Subject:      evidence.SubjectRef{Kind: "service", Name: caller},
			ContractRef:  ref,
			Source:       collector,
			ObservedAt:   opts.ObservedAt,
			Observations: obs,
		})
	}
	return sets
}

// Observe parses OTLP/JSON traces and returns the EvidenceSets they imply.
func Observe(data []byte, opts Options) ([]evidence.EvidenceSet, error) {
	td, err := ParseTraces(data)
	if err != nil {
		return nil, err
	}
	return EvidenceSets(DependencyEdges(td), opts), nil
}

// attrValue returns the string value of attribute key, or "".
func attrValue(attrs []KeyValue, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	return ""
}

// calleeName returns the callee identity of a span using calleeKeys precedence.
func calleeName(attrs []KeyValue) string {
	for _, k := range calleeKeys {
		if v := attrValue(attrs, k); v != "" {
			return v
		}
	}
	return ""
}
