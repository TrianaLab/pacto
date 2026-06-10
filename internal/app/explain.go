package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/readiness"
)

// timeNow is the clock used to derive readiness freshness. It is a variable so
// tests can pin "now" for deterministic readiness status.
var timeNow = time.Now

// ExplainOptions holds options for the explain command.
type ExplainOptions struct {
	Path      string
	Overrides override.Overrides
}

// ExplainResult holds the result of the explain command.
type ExplainResult struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Owner        contract.Owner         `json:"owner,omitempty"`
	PactoVersion string                 `json:"pactoVersion"`
	Runtime      ExplainRuntime         `json:"runtime"`
	Interfaces   []ExplainInterface     `json:"interfaces,omitempty"`
	Dependencies []ExplainDependency    `json:"dependencies,omitempty"`
	Scaling      *contract.Scaling      `json:"scaling,omitempty"`
	Readiness    *ExplainReadiness      `json:"readiness,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// ExplainReadiness is a derived readiness summary for the explain output.
type ExplainReadiness struct {
	Score         int                     `json:"score"`
	MinScore      int                     `json:"minScore"`
	Passing       bool                    `json:"passing"`
	TotalWeight   int                     `json:"totalWeight"`
	CurrentWeight int                     `json:"currentWeight"`
	CurrentCount  int                     `json:"currentCount"`
	ExpiredCount  int                     `json:"expiredCount"`
	InvalidCount  int                     `json:"invalidCount,omitempty"`
	Checks        []ExplainReadinessCheck `json:"checks"`
}

// ExplainReadinessCheck is a single derived readiness check for the explain output.
type ExplainReadinessCheck struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Weight   int    `json:"weight"`
	Expires  string `json:"expires"`
	Evidence string `json:"evidence,omitempty"`
}

// ExplainRuntime is a simplified runtime summary.
type ExplainRuntime struct {
	WorkloadType    string `json:"workloadType"`
	StateType       string `json:"stateType"`
	Scope           string `json:"scope"`
	Durability      string `json:"durability"`
	DataCriticality string `json:"dataCriticality"`
}

// ExplainInterface is a simplified interface summary.
type ExplainInterface struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Port       *int   `json:"port,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// ExplainDependency is a simplified dependency summary.
type ExplainDependency struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
}

// Explain produces a human-readable summary of a contract.
func (s *Service) Explain(ctx context.Context, opts ExplainOptions) (*ExplainResult, error) {
	ref := defaultPath(opts.Path)

	slog.Debug("resolving contract for explain", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}
	slog.Debug("explaining contract", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)

	c := bundle.Contract

	result := &ExplainResult{
		Name:         c.Service.Name,
		Version:      c.Service.Version,
		Owner:        c.Service.Owner,
		PactoVersion: c.PactoVersion,
		Scaling:      c.Scaling,
		Metadata:     c.Metadata,
	}

	if c.Runtime != nil {
		result.Runtime = ExplainRuntime{
			WorkloadType:    c.Runtime.Workload,
			StateType:       c.Runtime.State.Type,
			Scope:           c.Runtime.State.Persistence.Scope,
			Durability:      c.Runtime.State.Persistence.Durability,
			DataCriticality: c.Runtime.State.DataCriticality,
		}
	}

	for _, iface := range c.Interfaces {
		result.Interfaces = append(result.Interfaces, ExplainInterface{
			Name:       iface.Name,
			Type:       iface.Type,
			Port:       iface.Port,
			Visibility: iface.Visibility,
		})
	}

	for _, dep := range c.Dependencies {
		result.Dependencies = append(result.Dependencies, ExplainDependency{
			Name:          dep.Name,
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
		})
	}

	if eval := readiness.Evaluate(c.Readiness, timeNow()); eval != nil {
		er := &ExplainReadiness{
			Score:         eval.Score,
			MinScore:      eval.MinScore,
			Passing:       eval.Passing,
			TotalWeight:   eval.TotalWeight,
			CurrentWeight: eval.CurrentWeight,
			CurrentCount:  eval.CurrentCount,
			ExpiredCount:  eval.ExpiredCount,
			InvalidCount:  eval.InvalidCount,
		}
		for _, ch := range eval.Checks {
			er.Checks = append(er.Checks, ExplainReadinessCheck{
				ID:       ch.ID,
				Type:     ch.Type,
				Status:   string(ch.Status),
				Weight:   ch.Weight,
				Expires:  ch.Expires,
				Evidence: ch.Evidence,
			})
		}
		result.Readiness = er
	}

	return result, nil
}
