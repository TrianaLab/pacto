package fleetsrc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/strictjson"
)

// maxEvidenceBodyBytes bounds the response body an evidence source reads, so a
// misbehaving or hostile server cannot exhaust memory. The server already caps
// the target count; this is a defensive second bound.
const maxEvidenceBodyBytes = 4 << 20 // 4 MiB

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
func (s *EvidenceHTTPSource) Kind() string { return "evidence-http" }

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
	// The server's own DTO type, not a hand-kept mirror of it: the two copies had
	// already drifted to different Coverage types for the same JSON, and a strict
	// decode (unknown fields rejected) turns any further drift into a hard failure
	// at a consumer that has no way to know what it dropped. The boundary the old
	// comment cited does not exist -- internal may import pkg, and internal/app
	// already imports this package -- and only this direction is importable, since
	// pkg/evidenceingest may not reach internal.
	var body evidenceingest.TargetsResponse
	// Strict: reject unknown fields AND trailing data, bounded by the LimitReader.
	if err := strictjson.Decode(io.LimitReader(resp.Body, maxEvidenceBodyBytes), &body); err != nil {
		return nil, err
	}
	if body.SchemaVersion != evidenceingest.TargetsSchemaVersion {
		return nil, fmt.Errorf("evidence source %s speaks schema %q, want %q", url, body.SchemaVersion, evidenceingest.TargetsSchemaVersion)
	}

	col := &fleet.Collection{}
	for _, t := range body.Targets {
		if !fleet.ValidStatus(t.Compliance) {
			// A record with a status this consumer cannot interpret is kept out of
			// the graph but surfaced, so a bad record is never confused with none.
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: s.id,
				Message: fmt.Sprintf("evidence target %q reported unknown compliance status", t.Subject),
			})
			continue
		}
		evidenceAt, acceptedAt := t.EvidenceAt, t.AcceptedAt
		coverage := fleet.Coverage{Evaluated: t.Coverage.Evaluated, Required: t.Coverage.Required}
		// Name is the operational target (subject); Service/Domain/Digest are the
		// RESOLVED logical identity the server derived from the ContractRef — used
		// as-is, never inferred from Subject, so the target links to the correct
		// domain-qualified service and revision. Fall back to deriving the digest
		// from the ref only when the server omitted it.
		digest := t.Digest
		if digest == "" {
			digest = digestFromRef(t.ContractRef)
		}
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope:        t.Producer,
			Kind:         "external",
			Name:         t.Subject,
			Service:      t.Service,
			Domain:       t.Domain,
			ResolvedRef:  t.ContractRef,
			Digest:       digest,
			Compliance:   t.Compliance,
			Findings:     t.Findings,
			Coverage:     &coverage,
			EvidenceAt:   &evidenceAt,
			ReconciledAt: &acceptedAt,
		})
	}

	// An unreadable subject, an invalid published artifact or a truncated response
	// means the contribution is incomplete: surface the limitation so downstream
	// answers carry the honesty rather than presenting a full-looking graph.
	//
	// The limitation is the WHOLE mechanism. [fleet.Build] already downgrades a
	// source with collection limitations to partial, and it does so on the path
	// that also stamps LastSuccessfulSync and ObservedAt. Declaring the state here
	// instead took the source-declared branch, which copies the state verbatim, so
	// saying "partial" cost the source both of its freshness timestamps and a
	// degraded server read as one that had never synced.
	if degraded, msg := evidenceDegraded(body); degraded {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: fleet.LimitationSourcePartial, Source: s.id, Message: msg,
		})
	}
	return col, nil
}

// evidenceDegraded reports whether the server's contribution is incomplete and a
// sanitized reason. Any non-ready status, unreadable subject, invalid published
// artifact or truncated body counts. The counts are read independently of the
// status so a server that reports them without downgrading its own status still
// makes the consumer honest.
//
// The prose comes from [evidenceingest.SourceHealth.Reason], the server's own
// summary of its own health block. A second phrasing here had already drifted
// from it -- it had no wording for unreadable subjects AND invalid artifacts
// together, and reported only the first. Truncation is the one thing Reason
// cannot say, because it is a property of the response, not of the store read.
func evidenceDegraded(body evidenceingest.TargetsResponse) (bool, string) {
	h := body.Health
	switch {
	case h.FailedSubjects > 0 || h.InvalidArtifacts > 0 || (h.Status != "" && h.Status != evidenceingest.HealthReady):
		return true, h.Reason()
	case body.Truncated:
		return true, "evidence source response was truncated"
	}
	return false, ""
}
