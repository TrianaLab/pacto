package app

import (
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
)

// ObserveOTel parses OTLP/JSON trace data and returns both the derived
// dependency edges and the EvidenceSets they imply (one per calling service).
// The edges are the raw observed graph; the EvidenceSets are the signable,
// reportable form for the external evidence protocol.
func (s *Service) ObserveOTel(data []byte, opts otelobserver.Options) ([]otelobserver.Edge, []evidence.EvidenceSet, error) {
	td, err := otelobserver.ParseTraces(data)
	if err != nil {
		return nil, nil, err
	}
	edges := otelobserver.DependencyEdges(td)
	return edges, otelobserver.EvidenceSets(edges, opts), nil
}
