package collector

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v2/pkg/evidence"
)

// StaticCollector is a reference collector built from preset observations.
// It demonstrates the Collector SDK producing a valid, validated EvidenceSet.
type StaticCollector struct {
	contractRef  string
	source       string
	observations []evidence.Observation
}

// NewStaticCollector constructs a static collector with preset observations.
func NewStaticCollector(contractRef, source string, observations []evidence.Observation) *StaticCollector {
	return &StaticCollector{
		contractRef:  contractRef,
		source:       source,
		observations: observations,
	}
}

// Collect returns a preset EvidenceSet for the given subject.
func (c *StaticCollector) Collect(ctx context.Context, subject evidence.SubjectRef) (evidence.EvidenceSet, error) {
	return evidence.EvidenceSet{
		Subject:      subject,
		ContractRef:  c.contractRef,
		Source:       c.source,
		ObservedAt:   time.Now(),
		Observations: c.observations,
	}, nil
}
