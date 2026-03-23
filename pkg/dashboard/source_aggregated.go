package dashboard

import (
	"context"
	"fmt"
	"log"
	"sort"
)

// sourcePriority defines the merge priority for overlapping data.
// Lower number = higher priority for that domain.
var sourcePriority = map[string]int{
	"k8s":   0, // highest for runtime state
	"local": 1, // highest for in-progress work
	"oci":   2, // highest for version history
	"cache": 3, // offline cache, same role as oci but lower priority
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

func (a *AggregatedSource) ListServices(ctx context.Context) ([]Service, error) {
	// Collect services from all sources concurrently.
	type sourceResult struct {
		sourceType string
		services   []Service
		err        error
	}

	results := make(chan sourceResult, len(a.sources))
	for st, ds := range a.sources {
		go func() {
			svcs, err := ds.ListServices(ctx)
			results <- sourceResult{sourceType: st, services: svcs, err: err}
		}()
	}

	// Group by service name.
	byName := make(map[string]*aggregatedEntry)

	for range a.sources {
		r := <-results
		if r.err != nil {
			log.Printf("[dashboard] source %q ListServices failed: %v", r.sourceType, r.err)
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
	// Prefer OCI for version history, then cache, then fall back to other sources.
	for _, sourceType := range []string{"oci", "cache", "local", "k8s"} {
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
	type sourceResult struct {
		sourceType string
		details    *ServiceDetails
		err        error
	}

	results := make(chan sourceResult, len(a.sources))
	for st, ds := range a.sources {
		go func() {
			d, err := ds.GetService(ctx, name)
			results <- sourceResult{sourceType: st, details: d, err: err}
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

	// Sort sources by priority for deterministic merge.
	sort.Slice(agg.Sources, func(i, j int) bool {
		return sourcePriority[agg.Sources[i].SourceType] < sourcePriority[agg.Sources[j].SourceType]
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

	// Apply priority: k8s > local > oci > cache for phase/version/owner.
	for _, st := range []string{"cache", "oci", "local", "k8s"} {
		svc, ok := e.sources[st]
		if !ok {
			continue
		}
		if svc.Version != "" {
			merged.Version = svc.Version
		}
		if svc.Owner != "" {
			merged.Owner = svc.Owner
		}
		if svc.Phase != PhaseUnknown && svc.Phase != "" {
			merged.Phase = svc.Phase
		}
		merged.Source = st // last applied = highest priority
	}

	return merged
}

// mergeServiceDetails merges per-source details using priority rules.
// Sources must be sorted by priority (lowest index = highest priority).
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

	// Apply priority overrides.
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

// mergeFromLocal applies local contract overrides.
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
	if len(d.Dependencies) > 0 {
		merged.Dependencies = d.Dependencies
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

func mergeBaselineContract(merged *ServiceDetails, d *ServiceDetails) {
	if merged.Interfaces == nil && len(d.Interfaces) > 0 {
		merged.Interfaces = d.Interfaces
	}
	if merged.Configuration == nil && d.Configuration != nil {
		merged.Configuration = d.Configuration
	}
	if merged.Dependencies == nil && len(d.Dependencies) > 0 {
		merged.Dependencies = d.Dependencies
	}
	if merged.Policy == nil && d.Policy != nil {
		merged.Policy = d.Policy
	}
	if merged.Runtime == nil && d.Runtime != nil {
		merged.Runtime = d.Runtime
	}
}
