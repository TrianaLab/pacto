package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/override"
	"github.com/trianalab/pacto/v2/pkg/readiness"
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
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Owner        contract.Owner      `json:"owner,omitempty"`
	PactoVersion string              `json:"pactoVersion"`
	Runtime      ExplainRuntime      `json:"runtime"`
	Interfaces   []ExplainInterface  `json:"interfaces,omitempty"`
	Dependencies []ExplainDependency `json:"dependencies,omitempty"`
	Scaling      *contract.Scaling   `json:"scaling,omitempty"`
	Readiness    *ExplainReadiness   `json:"readiness,omitempty"`
	Metadata     map[string]any      `json:"metadata,omitempty"`
}

// ExplainReadiness is a derived readiness summary for the explain output.
type ExplainReadiness struct {
	Score         int                        `json:"score"`
	MinScore      int                        `json:"minScore"`
	Passing       bool                       `json:"passing"`
	TotalWeight   int                        `json:"totalWeight"`
	EarnedWeight  int                        `json:"earnedWeight"`
	PartialCredit float64                    `json:"partialCredit"`
	Expires       string                     `json:"expires"`
	Expired       bool                       `json:"expired"`
	DaysRemaining *int                       `json:"daysRemaining,omitempty"`
	DoneCount     int                        `json:"doneCount"`
	PartialCount  int                        `json:"partialCount"`
	NotDoneCount  int                        `json:"notDoneCount"`
	DeferredCount int                        `json:"deferredCount"`
	Revisions     []ExplainReadinessRevision `json:"revisions,omitempty"`
	Checks        []ExplainReadinessCheck    `json:"checks"`
}

// ExplainReadinessCheck is a single derived readiness check for the explain output.
type ExplainReadinessCheck struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Category     string `json:"category,omitempty"`
	Status       string `json:"status"`
	Weight       int    `json:"weight"`
	EarnedWeight int    `json:"earnedWeight"`
	Excluded     bool   `json:"excluded"`
	Evidence     string `json:"evidence,omitempty"`
}

// ExplainReadinessRevision is a single readiness revision-history entry.
type ExplainReadinessRevision struct {
	Date        string `json:"date"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
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
			EarnedWeight:  eval.EarnedWeight,
			PartialCredit: eval.PartialCredit,
			Expires:       eval.Expires,
			Expired:       eval.Expired,
			DaysRemaining: eval.DaysRemaining,
			DoneCount:     eval.DoneCount,
			PartialCount:  eval.PartialCount,
			NotDoneCount:  eval.NotDoneCount,
			DeferredCount: eval.DeferredCount,
		}
		for _, ch := range eval.Checks {
			er.Checks = append(er.Checks, ExplainReadinessCheck{
				ID:           ch.ID,
				Type:         ch.Type,
				Category:     ch.Category,
				Status:       ch.Status,
				Weight:       ch.Weight,
				EarnedWeight: ch.EarnedWeight,
				Excluded:     ch.Excluded,
				Evidence:     ch.Evidence,
			})
		}
		for _, rev := range c.Readiness.History {
			er.Revisions = append(er.Revisions, ExplainReadinessRevision{
				Date:        rev.Date,
				Version:     rev.Version,
				Author:      rev.Author,
				Description: rev.Description,
			})
		}
		result.Readiness = er
	}

	return result, nil
}
