package fleetsrc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// EvidenceHTTPSource consumes an Evidence Server's read-only Operational Graph
// contribution over HTTP, WITHOUT touching its durable store — the consumer
// never gets registry credentials or enumerates referrers itself. It GETs the
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
func (s *EvidenceHTTPSource) Kind() string { return fleet.SourceKindEvidenceHTTP }

// Collect GETs the server's read-only targets projection and maps each into an
// external fleet target. Any transport, status or decode failure is returned so
// the source is recorded as unavailable rather than empty — including the 503 a
// server returns when it could read none of its configured subjects. A partial
// or truncated server yields a SourcePartial collection (usable targets kept,
// the limitation surfaced), never a silently-healthy-looking empty one.
func (s *EvidenceHTTPSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	url := s.baseURL + evidenceingest.TargetsPath
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
	// The shared mapping decodes strictly on the server's own DTO type, so the
	// target snapshot reader and this source never drift. A body cut at the
	// bound, or by a failed read, fails that decode.
	data, _ := io.ReadAll(io.LimitReader(resp.Body, evidenceingest.MaxTargetsBodyBytes))
	col, err := evidenceingest.TargetsCollection(s.id, data)
	if err != nil {
		return nil, fmt.Errorf("evidence source %s: %w", url, err)
	}
	col.Providers = s.adapterRows(ctx)
	return col, nil
}

// adapterRows are the reporting adapter rows of the server's GET /producers:
// one per producer its trust config allows to report provisioning or usage.
// ponytail: a server that lists no adapters (an older one) or a read that
// fails gives no row and no limitation; a missing row claims nothing, and the
// targets the source exists for were read.
func (s *EvidenceHTTPSource) adapterRows(ctx context.Context) []fleet.Provider {
	// The targets request on the same base URL was already built.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+evidenceingest.ProducersPath, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body evidenceingest.ProducersResponse
	if resp.StatusCode != http.StatusOK || strictjson.Decode(io.LimitReader(resp.Body, evidenceingest.MaxTargetsBodyBytes), &body) != nil {
		return nil
	}
	var rows []fleet.Provider
	for _, a := range body.Adapters {
		rows = append(rows, fleet.Provider{ID: "reporting-producer/" + a.Producer, Name: a.Producer, UsedFor: []string{fleet.UseReporting},
			Evidence: "the trust config allows it to report " + strings.Join(a.Kinds, " and ")})
	}
	return rows
}
