package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// evidenceTargetsPath is the read-only projection an Evidence Server exposes.
const evidenceTargetsPath = "/api/evidence/v1/targets"

// maxEvidenceBodyBytes bounds the response body an evidence source reads, so a
// misbehaving or hostile server cannot exhaust memory. The server already caps
// the target count; this is a defensive second bound.
const maxEvidenceBodyBytes = 4 << 20 // 4 MiB

// EvidenceHTTPSource consumes an Evidence Server's read-only Operational Graph
// contribution over HTTP, WITHOUT touching its durable bucket. It GETs the
// server's /targets projection and maps each accepted target into an external
// fleet target. A transport failure or non-200 response is returned as an error
// so [fleet.Build] records the source as unavailable — never as an empty result
// that would silently drop a whole environment from the graph.
type EvidenceHTTPSource struct {
	id      string
	baseURL string
	client  *http.Client
}

// NewEvidenceHTTPSource returns a read-only HTTP evidence source over baseURL.
// The client has a short timeout; it is a field so tests can inject their own.
func NewEvidenceHTTPSource(id, baseURL string) *EvidenceHTTPSource {
	if id == "" {
		id = "evidence-http"
	}
	return &EvidenceHTTPSource{id: id, baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

// ID implements [fleet.Source].
func (s *EvidenceHTTPSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *EvidenceHTTPSource) Kind() string { return "evidence-http" }

// evidenceTargetsResponse mirrors the server's read-only /targets projection.
// Only the fields the fleet target needs are decoded; findingsCount is a summary
// count, so the full findings are intentionally not reconstructed here.
type evidenceTargetsResponse struct {
	Targets []struct {
		Subject    string         `json:"subject"`
		Producer   string         `json:"producer"`
		Compliance string         `json:"compliance"`
		Coverage   fleet.Coverage `json:"coverage"`
		ObservedAt time.Time      `json:"observedAt"`
	} `json:"targets"`
}

// Collect GETs the server's read-only targets projection and maps each into an
// external fleet target. Any transport, status or decode failure is returned so
// the source is recorded as unavailable rather than empty.
func (s *EvidenceHTTPSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	url := s.baseURL + evidenceTargetsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("evidence source %s returned HTTP %d", url, resp.StatusCode)
	}
	var body evidenceTargetsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEvidenceBodyBytes)).Decode(&body); err != nil {
		return nil, err
	}
	col := &fleet.Collection{}
	for _, t := range body.Targets {
		observedAt, coverage := t.ObservedAt, t.Coverage
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope:      t.Producer,
			Kind:       "external",
			Name:       t.Subject,
			Service:    t.Subject,
			Compliance: t.Compliance,
			Coverage:   &coverage,
			EvidenceAt: &observedAt,
		})
	}
	return col, nil
}
