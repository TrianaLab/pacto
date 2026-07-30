package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// evidenceTargetsPath is the read-only projection an Evidence Server exposes.
const evidenceTargetsPath = "/api/evidence/v1/targets"

// evidenceSchemaVersion is the read-only evidence-source DTO wire model this
// consumer understands. A server advertising a different schema is treated as
// unavailable rather than silently misread — the version is the compatibility
// contract, not a hint. It mirrors evidenceingest.TargetsSchemaVersion; the two
// packages are wired only over the wire, so the constant is duplicated rather
// than importing across the internal/pkg boundary.
const evidenceSchemaVersion = "pacto.dev/evidence-source/v1"

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

// evidenceTargetsResponse mirrors the server's versioned /targets DTO EXACTLY:
// the response is strictly decoded (unknown fields rejected), so any field the
// server adds bumps the schema version, which this consumer rejects rather than
// silently dropping. Every field a faithful fleet target needs — full findings,
// contract linkage, freshness, provenance and store health — is carried, so an
// external target is not a lossy summary.
type evidenceTargetsResponse struct {
	SchemaVersion string    `json:"schemaVersion"`
	GeneratedAt   time.Time `json:"generatedAt"`
	Health        struct {
		Phase         string `json:"phase"`
		PendingRepair bool   `json:"pendingRepair"`
		Corruptions   int    `json:"corruptions"`
	} `json:"health"`
	Truncated bool `json:"truncated"`
	Targets   []struct {
		Subject       string            `json:"subject"`
		Producer      string            `json:"producer"`
		ProducerKeyID string            `json:"producerKeyId"`
		Compliance    string            `json:"compliance"`
		Coverage      fleet.Coverage    `json:"coverage"`
		Findings      []finding.Finding `json:"findings"`
		ContractRef   string            `json:"contractRef"`
		EvidenceAt    time.Time         `json:"evidenceAt"`
		AcceptedAt    time.Time         `json:"acceptedAt"`
	} `json:"targets"`
}

// Collect GETs the server's read-only targets projection and maps each into an
// external fleet target. Any transport, status or decode failure is returned so
// the source is recorded as unavailable rather than empty. A degraded or
// truncated server yields a SourcePartial collection (usable targets kept, the
// limitation surfaced), never a silently-healthy-looking empty one.
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
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxEvidenceBodyBytes))
	dec.DisallowUnknownFields()
	var body evidenceTargetsResponse
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	if body.SchemaVersion != evidenceSchemaVersion {
		return nil, fmt.Errorf("evidence source %s speaks schema %q, want %q", url, body.SchemaVersion, evidenceSchemaVersion)
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
		evidenceAt, acceptedAt, coverage := t.EvidenceAt, t.AcceptedAt, t.Coverage
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope:        t.Producer,
			Kind:         "external",
			Name:         t.Subject,
			Service:      t.Subject,
			ResolvedRef:  t.ContractRef,
			Digest:       digestFromRef(t.ContractRef),
			Compliance:   t.Compliance,
			Findings:     t.Findings,
			Coverage:     &coverage,
			EvidenceAt:   &evidenceAt,
			ReconciledAt: &acceptedAt,
		})
	}

	// A degraded/recovering/corrupt store, or a truncated response, means the
	// contribution is incomplete: mark the source partial so downstream answers
	// carry the honesty rather than presenting a full-looking graph.
	if degraded, msg := evidenceDegraded(body); degraded {
		col.State = &fleet.SourceState{Status: fleet.SourcePartial}
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: fleet.LimitationSourcePartial, Source: s.id, Message: msg,
		})
	}
	return col, nil
}

// evidenceDegraded reports whether the server's contribution is incomplete and a
// sanitized reason. Any non-ready phase, pending repair, known corruptions or a
// truncated body counts.
func evidenceDegraded(body evidenceTargetsResponse) (bool, string) {
	switch {
	case body.Health.Phase != "" && body.Health.Phase != "ready":
		return true, fmt.Sprintf("evidence store phase %q", body.Health.Phase)
	case body.Health.PendingRepair:
		return true, "evidence store has pending projection repair"
	case body.Health.Corruptions > 0:
		return true, fmt.Sprintf("evidence store reported %d corrupt record(s)", body.Health.Corruptions)
	case body.Truncated:
		return true, "evidence source response was truncated"
	}
	return false, ""
}
