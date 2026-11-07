package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/readiness"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// ExplainOptions holds options for the explain command.
type ExplainOptions struct {
	Path      string
	Overrides override.Overrides
}

// ExplainResult holds the result of the explain command.
type ExplainResult struct {
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Owner    contract.Owner `json:"owner,omitempty"`
	Domain   string         `json:"domain,omitempty"`
	Offering string         `json:"offering,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	// Lifecycle is the service lifecycle (pactoVersion 2.6).
	Lifecycle    *contract.InterfaceLifecycle `json:"lifecycle,omitempty"`
	PactoVersion string                       `json:"pactoVersion"`
	Workload     string                       `json:"workload,omitempty"`
	State        *ExplainState                `json:"state,omitempty"`
	Capabilities []ExplainCapability          `json:"capabilities,omitempty"`
	Interfaces   []ExplainInterface           `json:"interfaces,omitempty"`
	Dependencies []ExplainDependency          `json:"dependencies,omitempty"`
	// Configurations lists each configuration with the values it requests.
	Configurations []ExplainConfiguration `json:"configurations,omitempty"`
	// Environments lists each environment and the file holding its values.
	Environments []contract.Environment `json:"environments,omitempty"`
	Readiness    *ExplainReadiness      `json:"readiness,omitempty"`
	Metadata     map[string]any         `json:"metadata,omitempty"`
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

// ExplainState is a simplified state summary.
type ExplainState struct {
	Type            string `json:"type"`
	Scope           string `json:"scope"`
	Durability      string `json:"durability"`
	DataCriticality string `json:"dataCriticality"`
}

// ExplainCapability is a simplified capability summary.
type ExplainCapability struct {
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"`
}

// ExplainInterface is a simplified interface summary.
type ExplainInterface struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Ref        string `json:"ref"`
	Visibility string `json:"visibility,omitempty"`
	// Lifecycle is the retirement schedule of a deprecated interface.
	Lifecycle *contract.InterfaceLifecycle `json:"lifecycle,omitempty"`
	// Guarantees is the behavior the interface promises its consumers, and
	// CompatibilityEpoch counts its incompatible behavior changes.
	Guarantees         *contract.Guarantees `json:"guarantees,omitempty"`
	CompatibilityEpoch int                  `json:"compatibilityEpoch,omitempty"`
}

// ExplainConfiguration is a configuration and the values it requests.
type ExplainConfiguration struct {
	Name          string `json:"name"`
	Schema        string `json:"schema,omitempty"`
	Ref           string `json:"ref,omitempty"`
	Configuration string `json:"configuration,omitempty"`
	Required      bool   `json:"required"`
	// Values are the inline values, the request where no environment applies.
	Values map[string]any `json:"values,omitempty"`
	// EnvironmentValues maps each environment to its effective values: Values
	// with that environment's file merged on top, objects key by key, a null
	// removing its key. An adapter provisions from these.
	EnvironmentValues map[string]any `json:"environmentValues,omitempty"`
}

// Summary is the configuration's one-line text form: its name, where its
// schema comes from and whether it is required.
func (c ExplainConfiguration) Summary() string {
	src := c.Schema
	if c.Ref != "" {
		src = c.Ref
		if c.Configuration != "" {
			src += ", configuration " + c.Configuration
		}
	}
	req := "optional"
	if c.Required {
		req = "required"
	}
	return c.Name + ": " + src + " (" + req + ")"
}

// ValueLines are the configuration's values as compact JSON, the inline ones
// first and then each environment's effective values by name.
func (c ExplainConfiguration) ValueLines() []string {
	var out []string
	if len(c.Values) > 0 {
		out = append(out, "values: "+compactJSON(c.Values))
	}
	for _, env := range slices.Sorted(maps.Keys(c.EnvironmentValues)) {
		out = append(out, env+": "+compactJSON(c.EnvironmentValues[env]))
	}
	return out
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v) // values decoded from YAML or JSON always marshal
	return string(b)
}

// ExplainDependency is a simplified dependency summary.
type ExplainDependency struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
	External      bool   `json:"external,omitempty"`
	Version       string `json:"version,omitempty"`
	// Requires is what the dependency relies on from the provider's
	// interfaces, as declared. Explain reads one contract, so it does not say
	// whether the provider meets it.
	Requires []contract.Requirement `json:"requires,omitempty"`
}

// Explain produces a human-readable summary of a contract.
func (s *Service) Explain(ctx context.Context, opts ExplainOptions) (*ExplainResult, error) {
	ref := defaultPath(opts.Path)

	logging.LoggerFromContext(ctx).Debug("resolving contract for explain", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}
	logging.LoggerFromContext(ctx).Debug("explaining contract", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)

	c := bundle.Contract

	result := &ExplainResult{
		Name:         c.Service.Name,
		Version:      c.Service.Version,
		Owner:        c.Service.Owner,
		Domain:       c.Service.Domain,
		Offering:     c.Service.Offering,
		Summary:      c.Service.Summary,
		Lifecycle:    c.Service.Lifecycle,
		PactoVersion: c.PactoVersion,
		Workload:     c.Workload,
		Environments: c.Environments,
		Metadata:     c.Metadata,
	}

	if c.State != nil {
		result.State = &ExplainState{
			Type:            c.State.Type,
			Scope:           c.State.Persistence.Scope,
			Durability:      c.State.Persistence.Durability,
			DataCriticality: c.State.DataCriticality,
		}
	}

	for _, cap := range c.Capabilities {
		result.Capabilities = append(result.Capabilities, ExplainCapability{
			Type: cap.Type,
			Ref:  cap.Ref,
		})
	}

	for _, iface := range c.Interfaces {
		result.Interfaces = append(result.Interfaces, ExplainInterface{
			Name:       iface.Name,
			Type:       iface.Type,
			Ref:        iface.Ref,
			Visibility: iface.Visibility,
			Lifecycle:  iface.Lifecycle,

			Guarantees:         iface.Guarantees,
			CompatibilityEpoch: iface.CompatibilityEpoch,
		})
	}

	for _, dep := range c.Dependencies {
		result.Dependencies = append(result.Dependencies, ExplainDependency{
			Name:          dep.Name,
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
			External:      dep.External,
			Version:       dep.Version,
			Requires:      dep.Requires,
		})
	}

	configs, err := explainConfigurations(c, bundle.FS)
	if err != nil {
		return nil, err
	}
	result.Configurations = configs

	if eval := readiness.Evaluate(c.Readiness, s.env().now()); eval != nil {
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

// explainConfigurations summarizes each configuration with its effective values
// in every environment. An environment file that cannot be read fails the
// explain: reporting the inline values alone would misstate the request.
func explainConfigurations(c *contract.Contract, bundleFS fs.FS) ([]ExplainConfiguration, error) {
	if len(c.Configurations) == 0 {
		return nil, nil
	}
	envValues := make([]map[string]any, len(c.Environments))
	for i, env := range c.Environments {
		if bundleFS == nil {
			return nil, fmt.Errorf("environment %s: no bundle files to read %s from", env.Name, env.File)
		}
		raw, err := fs.ReadFile(bundleFS, env.File)
		if err == nil {
			envValues[i], err = contract.ParseEnvironmentValues(raw)
		}
		if err != nil {
			return nil, fmt.Errorf("environment %s: cannot read %s: %w", env.Name, env.File, err)
		}
	}
	out := make([]ExplainConfiguration, 0, len(c.Configurations))
	for _, cfg := range c.Configurations {
		ec := ExplainConfiguration{
			Name: cfg.Name, Schema: cfg.Schema, Ref: cfg.Ref, Configuration: cfg.ProviderConfiguration,
			Required: cfg.Required, Values: cfg.Values,
		}
		for i, env := range c.Environments {
			if ec.EnvironmentValues == nil {
				ec.EnvironmentValues = map[string]any{}
			}
			ec.EnvironmentValues[env.Name] = validation.EffectiveValues(cfg.Values, envValues[i][cfg.Name])
		}
		out = append(out, ec)
	}
	return out, nil
}
