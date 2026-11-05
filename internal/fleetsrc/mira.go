package fleetsrc

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ponytail: 15m, 200000 spans and 10s are fixed; add flags when an operator
// needs another window.
const (
	miraRequest      = `{"from":"-15m","to":"now","max_spans":200000}`
	miraMaxSpans     = 200000
	maxMiraBodyBytes = 16 << 20 // 16 MiB; a value cut at the limit fails to decode
	maxMiraNameBytes = 253      // a DNS name; OTel service names are far shorter
)

// miraMap is the part of Mira's POST /api/v1/map answer this source reads. The
// decode is lenient (Mira is pre-1.0): unknown fields are ignored.
type miraMap struct {
	Map struct {
		Nodes      []miraNode `json:"nodes"`
		Edges      []miraEdge `json:"edges"`
		Unresolved int        `json:"unresolved"`
	} `json:"map"`
	Stats struct {
		RowsScanned int `json:"rows_scanned"`
	} `json:"stats"`
}

// miraNode is one resource (one instance) of Mira's service map; several keys
// can share a service name.
type miraNode struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// miraEdge counts calls between two node keys; From is "entry" for root spans.
type miraEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Calls int    `json:"calls"`
}

// MiraSource reads the service map of a Mira trace store and contributes the
// calls it saw as observed edges. Mira samples and sees only instrumented
// services, so its silence about a declared call is no evidence: the collection
// is ObservedUndeclaredOnly. A transport, status or decode failure is returned
// so [fleet.Build] records the source as unavailable (sanitized), never empty.
type MiraSource struct {
	id      string
	baseURL string
	client  *http.Client
}

// NewMiraSource returns a Mira source over baseURL. The client refuses
// redirects and times out after 10s, so a slow Mira delays a snapshot by at most
// that long.
func NewMiraSource(id, baseURL string) *MiraSource {
	if id == "" {
		id = "mira"
	}
	return &MiraSource{id: id, baseURL: baseURL, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// ID implements [fleet.Source].
func (s *MiraSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *MiraSource) Kind() string { return fleet.SourceKindMira }

// Collect posts the fixed window to /api/v1/map and normalizes the answer. A
// truncated, empty or partly unattributable map is reported through one
// SOURCE_PARTIAL limitation carrying counts only.
func (s *MiraSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/api/v1/map", bytes.NewReader([]byte(miraRequest)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A refused redirect surfaces here as its 3xx status.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("mira map answered %d", resp.StatusCode)
	}
	var m miraMap
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMiraBodyBytes)).Decode(&m); err != nil {
		return nil, err
	}
	edges, unattributed := normalizeMiraMap(m)
	col := &fleet.Collection{Observed: edges, ObservedUndeclaredOnly: true}
	if msg := miraPartial(m, unattributed); msg != "" {
		col.Limitations = []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Source: s.id, Message: msg}}
	}
	return col, nil
}

// normalizeMiraMap folds Mira's per-instance edges into one edge per pair of
// service names, sorted by (From, To). Root spans and calls within one service
// are dropped; a call touching a key missing from nodes, a name Mira cannot
// vouch for (see attributableMiraName) or counting no calls is dropped and
// counted, because a call Pacto cannot attribute is uncertainty. Sums saturate.
// FirstSeen/LastSeen stay zero: Mira gives no per-edge window.
func normalizeMiraMap(m miraMap) (edges []fleet.ObservedEdge, unattributed int) {
	names := make(map[string]string, len(m.Map.Nodes))
	for _, n := range m.Map.Nodes {
		if attributableMiraName(n.Name) {
			names[n.Key] = n.Name
		}
	}
	index := map[[2]string]int{}
	for _, e := range m.Map.Edges {
		if e.From == "entry" {
			continue
		}
		from, okFrom := names[e.From]
		to, okTo := names[e.To]
		switch {
		case !okFrom || !okTo || e.Calls <= 0:
			unattributed++
		case from == to:
		default:
			k := [2]string{from, to}
			if i, ok := index[k]; ok {
				edges[i].Count = min(edges[i].Count, math.MaxInt-e.Calls) + e.Calls
				continue
			}
			index[k] = len(edges)
			edges = append(edges, fleet.ObservedEdge{From: from, To: to, Count: e.Calls})
		}
	}
	slices.SortFunc(edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return edges, unattributed
}

// attributableMiraName reports whether a service name from Mira can enter the
// fleet: printable, at most maxMiraNameBytes, not "unknown", and not shaped like
// a k8s caller, a Kubernetes identity Mira cannot vouch for and whose namespace
// restricted views would trust.
func attributableMiraName(name string) bool {
	return name != "" && name != "unknown" && len(name) <= maxMiraNameBytes && !strings.HasPrefix(name, "k8s:") &&
		!strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) })
}

// miraPartial joins the reasons the map is incomplete, counts only (never
// names), or returns "" when it is complete.
func miraPartial(m miraMap, unattributed int) string {
	var clauses []string
	if m.Stats.RowsScanned >= miraMaxSpans {
		clauses = append(clauses, fmt.Sprintf("the read stopped at %d spans", miraMaxSpans))
	}
	if m.Map.Unresolved > 0 {
		clauses = append(clauses, fmt.Sprintf("%d spans have a parent Mira could not resolve", m.Map.Unresolved))
	}
	if unattributed > 0 {
		clauses = append(clauses, fmt.Sprintf("%d calls are not attributable to a named service", unattributed))
	}
	if len(m.Map.Nodes) == 0 {
		clauses = append(clauses, "Mira returned no spans in the last 15 minutes")
	}
	if len(clauses) == 0 {
		return ""
	}
	return "Mira's service map is incomplete: " + strings.Join(clauses, "; ")
}

// CheckMiraURL accepts an http(s) URL with a host and no userinfo, query or
// fragment (the map path is appended to it). Mira URLs are
// operator configuration only; the error names the scheme and host, never
// credentials or the raw value.
func CheckMiraURL(u string) error {
	p, err := url.Parse(u)
	if err != nil {
		return errors.New("mira source: not a valid URL")
	}
	switch {
	case p.Scheme != "http" && p.Scheme != "https":
		return fmt.Errorf("mira source: scheme %q is not http or https", p.Scheme)
	case p.Host == "":
		return errors.New("mira source: the URL has no host")
	case p.User != nil:
		return fmt.Errorf("mira source %s://%s: credentials in the URL are not allowed", p.Scheme, p.Hostname())
	case strings.ContainsAny(u, "?#"):
		return fmt.Errorf("mira source %s://%s: a query or fragment in the URL is not allowed", p.Scheme, p.Hostname())
	}
	return nil
}
