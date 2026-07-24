package collector

import (
	"context"

	"github.com/trianalab/pacto/v2/pkg/evidence"
)

// Collector produces evidence by observing a real system.
// Collectors are integrations external to the pure validation engine
// and form the boundary between operational contracts and runtime state.
type Collector interface {
	Collect(ctx context.Context, subject evidence.SubjectRef) (evidence.EvidenceSet, error)
}
