package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// Source priority by data category.
// Lower index = higher priority. Each category has its own order
// because different data types have different authoritative sources.

// runtimePriority governs phase, conditions, endpoints, resources, ports, scaling.
var runtimePriority = []string{"k8s", "local", "cache", "oci"}

// contentPriority governs interfaces, configuration, policy, dependencies, metadata.
var contentPriority = []string{"local", "k8s", "cache", "oci"}

// versionPriority governs version history (GetVersions).
// k8s is first because PactoRevision resources are the most authoritative
// when the operator is installed. Falls back to cache/oci for offline data.
var versionPriority = []string{"k8s", "cache", "oci", "local"}

// identityPriority governs version/owner/imageRef/chartRef on the summary.
// Local edits take precedence, then k8s (live), then registry baseline.
var identityPriority = []string{"local", "k8s", "oci", "cache"}

// sourcePriorityIndex returns 0 for highest priority, len for unknown.
func sourcePriorityIndex(order []string, sourceType string) int {
	for i, s := range order {
		if s == sourceType {
			return i
		}
	}
	return len(order)
}

// AggregatedSource implements DataSource by combining multiple sources.
// It groups services by name across all sources and merges their data
// using priority rules:
//   - k8s for runtime state (phase, resources, ports, endpoints)
//   - oci for version history
//   - local for in-progress state
type AggregatedSource struct {
	sources map[string]DataSource // keyed by source type
}

// NewAggregatedSource creates a data source that aggregates multiple sources.
func NewAggregatedSource(sources map[string]DataSource) *AggregatedSource {
	return &AggregatedSource{sources: sources}
}

// sourceListResult holds the result of a ListServices call from a single source.
type sourceListResult struct {
	sourceType string
	services   []Service
	err        error
}

// sourceDetailResult holds the result of a GetService call from a single source.
type sourceDetailResult struct {
	sourceType string
	details    *ServiceDetails
	err        error
}

func (a *AggregatedSource) ListServices(ctx context.Context) ([]Service, error) {
	// Collect services from all sources concurrently.
	results := make(chan sourceListResult, len(a.sources))
	for st, ds := range a.sources {
		go func() {
			svcs, err := ds.ListServices(ctx)
			results <- sourceListResult{sourceType: st, services: svcs, err: err}
		}()
	}

	// Group by service name.
	byName := make(map[string]*aggregatedEntry)

	for range a.sources {
		r := <-results
		if r.err != nil {
			slog.Warn("source ListServices failed", "source", r.sourceType, "error", r.err)
			continue
		}
		for _, svc := range r.services {
			entry, ok := byName[svc.Name]
			if !ok {
				entry = &aggregatedEntry{}
				byName[svc.Name] = entry
			}
			entry.add(r.sourceType, &svc)
		}
	}

	// Build merged service list.
	var services []Service
	for name, entry := range byName {
		merged := entry.mergedSummary(name)
		services = append(services, merged)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services, nil
}

func (a *AggregatedSource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	agg, err := a.aggregate(ctx, name)
	if err != nil {
		return nil, err
	}
	return agg.Merged, nil
}

func (a *AggregatedSource) GetVersions(ctx context.Context, name string) ([]Version, error) {
	for _, sourceType := range versionPriority {
		ds, ok := a.sources[sourceType]
		if !ok {
			continue
		}
		versions, err := ds.GetVersions(ctx, name)
		if err == nil && len(versions) > 0 {
			return versions, nil
		}
	}
	return nil, fmt.Errorf("no version history available for %q", name)
}

func (a *AggregatedSource) GetDiff(ctx context.Context, from, to Ref) (*DiffResult, error) {
	// Route to the appropriate source based on Ref.Source, or try OCI then local.
	if from.Source != "" {
		if ds, ok := a.sources[from.Source]; ok {
			return ds.GetDiff(ctx, from, to)
		}
	}
	for _, sourceType := range []string{"oci", "cache", "local"} {
		ds, ok := a.sources[sourceType]
		if !ok {
			continue
		}
		result, err := ds.GetDiff(ctx, from, to)
		if err == nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("diff not available for %q", from.Name)
}

// GetAggregated returns the full aggregated view with per-source breakdown.
func (a *AggregatedSource) GetAggregated(ctx context.Context, name string) (*AggregatedService, error) {
	return a.aggregate(ctx, name)
}

// SourceTypes returns the list of active source types.
func (a *AggregatedSource) SourceTypes() []string {
	types := make([]string, 0, len(a.sources))
	for st := range a.sources {
		types = append(types, st)
	}
	sort.Strings(types)
	return types
}

func (a *AggregatedSource) aggregate(ctx context.Context, name string) (*AggregatedService, error) {
	results := make(chan sourceDetailResult, len(a.sources))
	for st, ds := range a.sources {
		go func() {
			d, err := ds.GetService(ctx, name)
			results <- sourceDetailResult{sourceType: st, details: d, err: err}
		}()
	}

	agg := &AggregatedService{Name: name}
	var found bool

	for range a.sources {
		r := <-results
		if r.err != nil || r.details == nil {
			continue
		}
		found = true
		agg.Sources = append(agg.Sources, ServiceSourceData{
			SourceType: r.sourceType,
			Service:    r.details,
		})
	}

	if !found {
		return nil, fmt.Errorf("service %q not found in any source", name)
	}

	// Sort sources by runtime priority for deterministic merge.
	// The first source becomes the base; per-type merge functions then apply overrides.
	sort.Slice(agg.Sources, func(i, j int) bool {
		return sourcePriorityIndex(runtimePriority, agg.Sources[i].SourceType) <
			sourcePriorityIndex(runtimePriority, agg.Sources[j].SourceType)
	})

	agg.Merged = mergeServiceDetails(agg.Sources)
	return agg, nil
}

// aggregatedEntry collects per-source summaries during list aggregation.
// No mutex needed: results are consumed sequentially from a channel.
type aggregatedEntry struct {
	sources map[string]*Service
}

func (e *aggregatedEntry) add(sourceType string, svc *Service) {
	if e.sources == nil {
		e.sources = make(map[string]*Service)
	}
	e.sources[sourceType] = svc
}

func (e *aggregatedEntry) mergedSummary(name string) Service {
	merged := Service{Name: name, Phase: PhaseUnknown}

	var sourceTypes []string
	for st := range e.sources {
		sourceTypes = append(sourceTypes, st)
	}
	sort.Strings(sourceTypes)
	merged.Sources = sourceTypes

	// Phase uses runtime priority: k8s > local > cache > oci
	for _, st := range runtimePriority {
		if svc, ok := e.sources[st]; ok && svc.Phase != PhaseUnknown && svc.Phase != "" {
			merged.Phase = svc.Phase
			merged.Source = st
			break
		}
	}

	// Version/Owner use identity priority: local > k8s > oci > cache
	for _, st := range identityPriority {
		if svc, ok := e.sources[st]; ok && svc.Version != "" {
			merged.Version = svc.Version
			break
		}
	}
	for _, st := range identityPriority {
		if svc, ok := e.sources[st]; ok && svc.Owner != "" {
			merged.Owner = svc.Owner
			break
		}
	}

	// If Source wasn't set by phase (all unknown), use the highest-priority present source.
	if merged.Source == "" {
		for _, st := range runtimePriority {
			if _, ok := e.sources[st]; ok {
				merged.Source = st
				break
			}
		}
	}

	return merged
}

// mergeServiceDetails merges per-source details using per-category priority.
// Sources must be sorted by priority (lowest index = highest priority).
//
// Priority by category:
//   - Runtime (phase, conditions, endpoints, resources, ports): k8s > local > cache > oci
//   - Contract content (interfaces, configuration, policy): local > k8s > cache > oci
//   - Graph relationships (dependencies): union across all sources
//   - Identity (version, owner, imageRef, chartRef): local > k8s > oci > cache
func mergeServiceDetails(sources []ServiceSourceData) *ServiceDetails {
	if len(sources) == 0 {
		return nil
	}

	// Start with a copy of the highest-priority source.
	base := *sources[0].Service
	merged := &base

	// Collect all source types.
	var sourceTypes []string
	for _, s := range sources {
		sourceTypes = append(sourceTypes, s.SourceType)
	}
	merged.Sources = sourceTypes

	// Apply per-type overrides by category.
	for _, s := range sources {
		switch s.SourceType {
		case "k8s":
			mergeFromK8s(merged, s.Service)
		case "local":
			mergeFromLocal(merged, s.Service)
		case "oci", "cache":
			mergeFromBaseline(merged, s.Service)
		}
	}

	// Union graph relationships across all sources (dependencies, cross-references).
	merged.Dependencies = unionDependencies(sources)

	return merged
}

// mergeFromK8s applies k8s runtime state overrides.
func mergeFromK8s(merged *ServiceDetails, d *ServiceDetails) {
	if d.Phase != PhaseUnknown && d.Phase != "" {
		merged.Phase = d.Phase
	}
	if d.Runtime != nil {
		merged.Runtime = d.Runtime
	}
	if d.Resources != nil {
		merged.Resources = d.Resources
	}
	if d.Ports != nil {
		merged.Ports = d.Ports
	}
	if d.Validation != nil {
		merged.Validation = d.Validation
	}
	if len(d.Endpoints) > 0 {
		merged.Endpoints = d.Endpoints
	}
	if d.Scaling != nil {
		merged.Scaling = d.Scaling
	}
	if len(d.Conditions) > 0 {
		merged.Conditions = d.Conditions
	}
	if len(d.Insights) > 0 {
		merged.Insights = d.Insights
	}
	if d.ChecksSummary != nil {
		merged.ChecksSummary = d.ChecksSummary
	}
}

// mergeFromLocal applies local contract content overrides.
// Dependencies are handled separately by unionDependencies.
func mergeFromLocal(merged *ServiceDetails, d *ServiceDetails) {
	if d.Version != "" {
		merged.Version = d.Version
	}
	if len(d.Interfaces) > 0 {
		merged.Interfaces = d.Interfaces
	}
	if d.Configuration != nil {
		merged.Configuration = d.Configuration
	}
	if d.Policy != nil {
		merged.Policy = d.Policy
	}
}

// mergeFromBaseline fills in missing fields from OCI/cache sources.
func mergeFromBaseline(merged *ServiceDetails, d *ServiceDetails) {
	mergeBaselineIdentity(merged, d)
	mergeBaselineContract(merged, d)
}

func mergeBaselineIdentity(merged *ServiceDetails, d *ServiceDetails) {
	if merged.Version == "" && d.Version != "" {
		merged.Version = d.Version
	}
	if merged.Owner == "" && d.Owner != "" {
		merged.Owner = d.Owner
	}
	if merged.ImageRef == "" && d.ImageRef != "" {
		merged.ImageRef = d.ImageRef
	}
	if merged.ChartRef == "" && d.ChartRef != "" {
		merged.ChartRef = d.ChartRef
	}
}

// unionDependencies merges dependencies from all sources, deduplicating by ref.
// When the same ref appears in multiple sources, the first occurrence (by content
// priority: local > k8s > oci > cache) wins.
func unionDependencies(sources []ServiceSourceData) []DependencyInfo {
	seen := make(map[string]bool)
	var result []DependencyInfo

	// Iterate in content priority order.
	for _, st := range contentPriority {
		for _, s := range sources {
			if s.SourceType != st {
				continue
			}
			for _, dep := range s.Service.Dependencies {
				if !seen[dep.Ref] {
					seen[dep.Ref] = true
					result = append(result, dep)
				}
			}
		}
	}
	return result
}

// mergeBaselineContract fills in contract content from OCI/cache if not already set.
// Dependencies are handled separately by unionDependencies.
func mergeBaselineContract(merged *ServiceDetails, d *ServiceDetails) {
	if merged.Interfaces == nil && len(d.Interfaces) > 0 {
		merged.Interfaces = d.Interfaces
	}
	if merged.Configuration == nil && d.Configuration != nil {
		merged.Configuration = d.Configuration
	}
	if merged.Policy == nil && d.Policy != nil {
		merged.Policy = d.Policy
	}
	if merged.Runtime == nil && d.Runtime != nil {
		merged.Runtime = d.Runtime
	}
}
