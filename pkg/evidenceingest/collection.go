package evidenceingest

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// MaxTargetsBodyBytes bounds a targets body a consumer reads, so a misbehaving
// or hostile server cannot exhaust memory. The server already caps the target
// count; this is a defensive second bound.
const MaxTargetsBodyBytes = 4 << 20 // 4 MiB

// TargetsCollection maps a targets body (GET TargetsPath) to the fleet
// collection of source: each accepted target as an external fleet target, the
// provisioning and usage records as verified records, and a degraded or
// truncated body as a limitation. Every reader of the body calls it, the HTTP
// evidence source and the target snapshot reader alike, so the two readings
// never drift.
//
// The decode is strict on the server's own DTO type, not a hand-kept mirror of
// it: unknown fields and trailing data are refused, so any drift is a hard
// failure at a consumer that has no way to know what it dropped. A body of a
// schema it does not speak is an error too.
func TargetsCollection(source string, data []byte) (*fleet.Collection, error) {
	var body TargetsResponse
	if err := strictjson.Decode(bytes.NewReader(data), &body); err != nil {
		return nil, err
	}
	// v3 is v2 plus provisioning records, v4 is v3 plus usage records and v5
	// is v4 plus clusters, which an older body never carries.
	switch body.SchemaVersion {
	case TargetsSchemaVersion, TargetsSchemaVersionProvisioning, TargetsSchemaVersionUsage, TargetsSchemaVersionCluster:
	default:
		return nil, fmt.Errorf("speaks schema %q, want %q, %q, %q or %q", body.SchemaVersion,
			TargetsSchemaVersion, TargetsSchemaVersionProvisioning, TargetsSchemaVersionUsage, TargetsSchemaVersionCluster)
	}

	col := &fleet.Collection{}
	for _, t := range body.Targets {
		if !fleet.ValidStatus(t.Compliance) {
			// A record with a status this consumer cannot interpret is kept out of
			// the graph but surfaced, so a bad record is never confused with none.
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: source,
				Message: fmt.Sprintf("evidence target %q reported unknown compliance status", t.Subject),
			})
			continue
		}
		evidenceAt, acceptedAt := t.EvidenceAt, t.AcceptedAt
		coverage := fleet.Coverage{Evaluated: t.Coverage.Evaluated, Required: t.Coverage.Required}
		// Name is the operational target (subject); Service/Domain/Digest are the
		// RESOLVED logical identity the server derived from the ContractRef — used
		// as-is, never inferred from Subject, so the target links to the correct
		// domain-qualified service and revision.
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope:        t.Producer,
			Kind:         "external",
			Name:         t.Subject,
			Service:      t.Service,
			Domain:       t.Domain,
			ResolvedRef:  t.ContractRef,
			Digest:       digestOr(t.Digest, t.ContractRef),
			Compliance:   t.Compliance,
			Findings:     t.Findings,
			Coverage:     &coverage,
			EvidenceAt:   &evidenceAt,
			ReconciledAt: &acceptedAt,
			Cluster:      acceptingCluster(t.Cluster, body.Cluster),
		})
	}

	for _, p := range body.Provisioning {
		acceptedAt := p.AcceptedAt
		col.Provisioning = append(col.Provisioning, fleet.RawProvisioning{
			Service: p.Service, Domain: p.Domain, Digest: digestOr(p.Digest, p.ContractRef), Kind: p.Kind, Name: p.Name, Environment: p.Environment,
			Provider: p.Provider, ExternalRef: p.ExternalRef, Phase: p.Phase, Outcome: p.Outcome,
			Producer: p.Producer, ProducerKeyID: p.ProducerKeyID, ObservedAt: p.EvidenceAt, AcceptedAt: &acceptedAt, Verified: true,
		})
	}

	usage, lims := usageRecords(source, data, body.Usage)
	col.Usage, col.Limitations = usage, append(col.Limitations, lims...)

	// An unreadable subject, an invalid published artifact or a truncated body
	// means the contribution is incomplete. The limitation is the whole
	// mechanism: [fleet.Build] downgrades a source with collection limitations
	// to partial.
	if degraded, msg := targetsDegraded(body); degraded {
		col.Limitations = append(col.Limitations, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: source, Message: msg})
	}
	return col, nil
}

// digestOr is digest, or the digest of ref when the server omitted it.
func digestOr(digest, ref string) string {
	if digest != "" {
		return digest
	}
	_, d := contract.SplitOCIDigest(ref)
	return d
}

// acceptingCluster is the cluster that accepted a record, nil when unknown. The
// listing server's own cluster only names it when the ids are equal: a record
// another cluster accepted keeps its id alone, and is never given the listing
// server's cluster. [fleet.Build] validates both.
func acceptingCluster(id string, server *ClusterDTO) *fleet.Cluster {
	switch {
	case id == "":
		return nil
	case server != nil && server.ID == id:
		return &fleet.Cluster{ID: id, Name: server.Name, NameSource: server.NameSource}
	}
	return &fleet.Cluster{ID: id}
}

// targetsDegraded reports whether the server's contribution is incomplete and a
// sanitized reason. Any non-ready status, unreadable subject, invalid published
// artifact or truncated body counts. The counts are read independently of the
// status so a server that reports them without downgrading its own status still
// makes the consumer honest. The prose is [SourceHealth.Reason], the server's
// own summary; truncation is a property of the response it cannot say.
func targetsDegraded(body TargetsResponse) (bool, string) {
	h := body.Health
	switch {
	case h.FailedSubjects > 0 || h.InvalidArtifacts > 0 || (h.Status != "" && h.Status != HealthReady):
		return true, h.Reason()
	case body.Truncated:
		return true, "evidence source response was truncated"
	}
	return false, ""
}

// usageRecords maps the body's usage DTOs to verified raw records. The strict
// decode does not require keys, so data is read again for each record's raw
// metrics: one holding a metric with no value would read as a measured zero,
// and is dropped with a counted limitation instead.
func usageRecords(source string, data []byte, dtos []UsageDTO) ([]fleet.RawUsage, []fleet.Limitation) {
	var raw struct {
		Usage []json.RawMessage `json:"usage"`
	}
	_ = json.Unmarshal(data, &raw) // the strict decode already accepted data
	var out []fleet.RawUsage
	valueless := 0
	for i, u := range dtos {
		if i < len(raw.Usage) && evidence.CheckMetricValues(raw.Usage[i]) != nil {
			valueless++
			continue
		}
		acceptedAt := u.AcceptedAt
		out = append(out, fleet.RawUsage{
			Service: u.Service, Domain: u.Domain, Digest: digestOr(u.Digest, u.ContractRef), Kind: u.Kind, Name: u.Name, Environment: u.Environment,
			Provider: u.Provider, ExternalRef: u.ExternalRef, Metrics: u.Metrics, Cost: u.Cost, Outcome: u.Outcome,
			Producer: u.Producer, ProducerKeyID: u.ProducerKeyID, ObservedAt: u.EvidenceAt, AcceptedAt: &acceptedAt, Verified: true,
		})
	}
	if valueless == 0 {
		return out, nil
	}
	return out, []fleet.Limitation{{Code: fleet.LimitationSourceRecordInvalid, Source: source,
		Message: fmt.Sprintf("%d usage records hold a metric with no value and were dropped", valueless)}}
}
