package fleetsrc

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
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
	// Contributions reads the records of the producers the server
	// advertises as contributors; nil reads none and says so on their rows.
	Contributions ContributionReader
	// Freshness is the window past which a producer's newest report or
	// record reads stale; 0 checks no age.
	Freshness time.Duration
	// Now is the source's clock; nil is time.Now.
	Now func() time.Time
}

// MaxContributors is how many of the producers an Evidence Server
// advertises as contributors one read reads, sorted.
const MaxContributors = 32

// ContributionReader reads the records one producer an Evidence Server
// advertises contributes, filling f, and returns the limitations that make
// the source partial. The registry client lives behind it: an evidence
// consumer never reaches the registry itself (internal/app reads it).
type ContributionReader interface {
	Read(ctx context.Context, source string, c evidenceingest.ProducerContributor, f *fleet.ProducerFacts) ([]fleet.RawRevision, []fleet.Limitation)
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
	revs, lims, rows := s.producers(ctx, col.Targets)
	col.Revisions, col.Providers = append(col.Revisions, revs...), rows
	col.Limitations = append(col.Limitations, lims...)
	return col, nil
}

// producers reads the server's GET /producers once and returns the records
// its contributors hold, the limitations of what could not be read and one
// row per producer: what it reports, what its trust config allows and what
// it contributes. A server that lists nothing, or predates /producers (404),
// has no contributor. Any other failed /producers read makes the source
// partial (CONTRIBUTION_UNREAD): the contributors it would list are unknown,
// never absent.
func (s *EvidenceHTTPSource) producers(ctx context.Context, targets []fleet.RawTarget) ([]fleet.RawRevision, []fleet.Limitation, []fleet.Provider) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	facts := map[string]*fleet.ProducerFacts{}
	fact := func(p string) *fleet.ProducerFacts {
		if facts[p] == nil {
			facts[p] = &fleet.ProducerFacts{Producer: p}
		}
		return facts[p]
	}
	for _, t := range targets {
		if t.Scope != "" && t.EvidenceAt != nil {
			f := fact(t.Scope)
			f.NewestReport = fleet.NewestOf(f.NewestReport, t.EvidenceAt)
		}
	}
	body, err := s.readProducers(ctx)
	for _, a := range body.Adapters {
		fact(a.Producer).AdapterKinds = a.Kinds
	}
	revs, lims := s.readContributions(ctx, body.Contributors, fact)
	if err != nil {
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationContributionUnread, Source: s.id,
			Message: "the producer list could not be read (" + fleet.SanitizedReason(err) + "); the services producers contribute are unknown"})
	}
	var rows []fleet.Provider
	for _, p := range slices.Sorted(maps.Keys(facts)) {
		if row, ok := fleet.ProducerRow(*facts[p], now(), s.Freshness); ok {
			rows = append(rows, row)
		}
	}
	return revs, lims, rows
}

// readContributions reads the records of the first MaxContributors
// contributors, sorted, through s.Contributions.
func (s *EvidenceHTTPSource) readContributions(ctx context.Context, contributors []evidenceingest.ProducerContributor, fact func(string) *fleet.ProducerFacts) ([]fleet.RawRevision, []fleet.Limitation) {
	contributors = slices.SortedFunc(slices.Values(contributors), func(a, b evidenceingest.ProducerContributor) int {
		return cmp.Or(cmp.Compare(a.Producer, b.Producer), cmp.Compare(a.Repository, b.Repository))
	})
	var (
		revs []fleet.RawRevision
		lims []fleet.Limitation
	)
	if n := len(contributors); n > MaxContributors {
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationContributionUnread, Source: s.id,
			Message: fmt.Sprintf("%d producers contribute; the contributions of the %d past the first %d were not read", n, n-MaxContributors, MaxContributors)})
		contributors = contributors[:MaxContributors]
	}
	for _, c := range contributors {
		f := fact(c.Producer)
		if s.Contributions == nil {
			// One row per producer, naming every repository, as Read does.
			f.Repository = strings.TrimPrefix(f.Repository+" and "+c.Repository, " and ")
			if !slices.Contains(f.Problems, noContributionRegistry) {
				f.Problems = append(f.Problems, noContributionRegistry)
			}
			continue
		}
		r, l := s.Contributions.Read(ctx, s.id, c, f)
		revs, lims = append(revs, r...), append(lims, l...)
	}
	return revs, lims
}

// noContributionRegistry is why a row names contributions nobody reads.
const noContributionRegistry = "no registry is configured to read contributions"

// readProducers is the server's GET /producers body: empty with no error
// from a server that predates the route (404), empty with an error when it
// could not be read.
func (s *EvidenceHTTPSource) readProducers(ctx context.Context) (evidenceingest.ProducersResponse, error) {
	var body evidenceingest.ProducersResponse
	// The targets request on the same base URL was already built.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+evidenceingest.ProducersPath, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return body, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return body, nil
	case resp.StatusCode != http.StatusOK:
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := strictjson.Decode(io.LimitReader(resp.Body, evidenceingest.MaxTargetsBodyBytes), &body); err != nil {
		return evidenceingest.ProducersResponse{}, err
	}
	return body, nil
}
