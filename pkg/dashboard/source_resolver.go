package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// ResolvedSource implements DataSource by combining a contract source
// (local or OCI/cache) with an optional runtime source (k8s).
//
// Resolution model:
//   - Contract: exactly ONE authoritative snapshot per service.
//     local wins over OCI. Cache is internal to OCI (not a separate source).
//   - Runtime: k8s enriches with phase, conditions, endpoints, resources,
//     ports, scaling, insights, checks. Never overrides contract fields.
//   - History: merged across all sources, labeled by origin.
//   - Diff: only works with real contract bundles (local or OCI/cache).
//   - Graph: built from the authoritative contract snapshot only.
type ResolvedSource struct {
	mu            sync.RWMutex
	contract      []namedContractSource // ordered: local first, then oci
	runtime       *runtimeSourceEntry   // optional k8s
	all           map[string]DataSource // all sources for version/diff lookups
	lastSourceErr map[string]string     // dedup: last error message per source type
}

type namedContractSource struct {
	name   string // "local" or "oci"
	source DataSource
}

type runtimeSourceEntry struct {
	source DataSource
}

// NewResolvedSource creates a data source with the new resolution model.
// contractSources: ordered by priority (local first, then oci).
// runtimeSource: optional k8s source for runtime enrichment.
// allSources: map of all sources for version history and diff fallback.
func NewResolvedSource(contractSources []namedContractSource, runtimeSource DataSource, allSources map[string]DataSource) *ResolvedSource {
	rs := &ResolvedSource{
		contract: contractSources,
		all:      allSources,
	}
	if runtimeSource != nil {
		rs.runtime = &runtimeSourceEntry{source: runtimeSource}
	}
	return rs
}

// sources returns a consistent snapshot of the current source configuration.
// Safe for concurrent use: AddContractSource uses copy-on-write, so the
// returned slices/map remain valid even if a new source is added concurrently.
func (r *ResolvedSource) sources() ([]namedContractSource, *runtimeSourceEntry, map[string]DataSource) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contract, r.runtime, r.all
}

// logSourceError logs a source error only when the message changes,
// preventing repeated identical warnings from flooding the terminal.
func (r *ResolvedSource) logSourceError(sourceType string, err error) {
	msg := err.Error()
	r.mu.Lock()
	if r.lastSourceErr == nil {
		r.lastSourceErr = make(map[string]string)
	}
	prev := r.lastSourceErr[sourceType]
	r.lastSourceErr[sourceType] = msg
	r.mu.Unlock()
	if msg != prev {
		slog.Warn("source ListServices failed", "source", sourceType, "error", err)
	}
}

// HasSource reports whether a source with the given name is registered.
func (r *ResolvedSource) HasSource(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.all[name]
	return ok
}

// GetSource returns the DataSource registered under the given name, or nil.
func (r *ResolvedSource) GetSource(name string) DataSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.all[name]
}

// AddContractSource registers a new contract data source that participates in
// service detail resolution and version/diff lookups. Thread-safe via
// copy-on-write: concurrent readers see a consistent snapshot.
func (r *ResolvedSource) AddContractSource(name string, ds DataSource) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newAll := make(map[string]DataSource, len(r.all)+1)
	for k, v := range r.all {
		newAll[k] = v
	}
	newAll[name] = ds
	r.all = newAll

	newContract := make([]namedContractSource, len(r.contract)+1)
	copy(newContract, r.contract)
	newContract[len(r.contract)] = namedContractSource{name: name, source: ds}
	r.contract = newContract
}

// AddSource registers a data source for version/diff lookups only
// (not contract resolution). Thread-safe via copy-on-write.
func (r *ResolvedSource) AddSource(name string, ds DataSource) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newAll := make(map[string]DataSource, len(r.all)+1)
	for k, v := range r.all {
		newAll[k] = v
	}
	newAll[name] = ds
	r.all = newAll
}

// sourceListResult holds the result of a ListServices call from a single source.
type sourceListResult struct {
	sourceType string
	services   []Service
	err        error
}

// serviceEntry groups data from multiple sources for a single service during ListServices.
type serviceEntry struct {
	contract *Service // from highest-priority contract source
	runtime  *Service // from k8s
	sources  []string
}

// mergeServiceEntry builds a merged Service from grouped source data.
func mergeServiceEntry(name string, entry *serviceEntry) Service {
	merged := Service{Name: name, Phase: PhaseUnknown}
	sort.Strings(entry.sources)
	merged.Sources = entry.sources

	if entry.contract != nil {
		merged.Version = entry.contract.Version
		merged.Owner = entry.contract.Owner
		merged.Source = entry.contract.Source
	}

	if entry.runtime != nil {
		if entry.runtime.Phase != PhaseUnknown && entry.runtime.Phase != "" {
			merged.Phase = entry.runtime.Phase
		}
		if merged.Source == "" {
			merged.Source = "k8s"
		}
	} else if entry.contract != nil {
		merged.Phase = entry.contract.Phase
	}

	return merged
}

func (r *ResolvedSource) ListServices(ctx context.Context) ([]Service, error) {
	_, _, all := r.sources()

	// Collect services from all sources concurrently.
	results := make(chan sourceListResult, len(all))
	for st, ds := range all {
		go func() {
			svcs, err := ds.ListServices(ctx)
			results <- sourceListResult{sourceType: st, services: svcs, err: err}
		}()
	}

	byName := make(map[string]*serviceEntry)

	for range all {
		res := <-results
		if res.err != nil {
			r.logSourceError(res.sourceType, res.err)
			continue
		}
		for _, svc := range res.services {
			entry, ok := byName[svc.Name]
			if !ok {
				entry = &serviceEntry{}
				byName[svc.Name] = entry
			}
			entry.sources = append(entry.sources, res.sourceType)

			if res.sourceType == "k8s" {
				entry.runtime = &svc
			} else if entry.contract == nil || isHigherContractPriority(res.sourceType, entry.contract.Source) {
				s := svc
				entry.contract = &s
			}
		}
	}

	// Build merged service list.
	var services []Service
	for name, entry := range byName {
		services = append(services, mergeServiceEntry(name, entry))
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services, nil
}

func (r *ResolvedSource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	contract, runtime, _ := r.sources()

	// Step 1: Resolve contract snapshot — exactly one winner.
	var contractDetails *ServiceDetails
	var contractSource string
	for _, cs := range contract {
		d, err := cs.source.GetService(ctx, name)
		if err == nil && d != nil {
			contractDetails = d
			contractSource = cs.name
			break
		}
	}

	// Step 2: Get runtime data from k8s.
	var runtimeDetails *ServiceDetails
	if runtime != nil {
		d, err := runtime.source.GetService(ctx, name)
		if err == nil && d != nil {
			runtimeDetails = d
		}
	}

	if contractDetails == nil && runtimeDetails == nil {
		return nil, fmt.Errorf("service %q not found in any source", name)
	}

	// Step 3: Build result.
	var result *ServiceDetails
	if contractDetails != nil {
		// Start with contract as base.
		base := *contractDetails
		result = &base
		result.Source = contractSource

		// Enrich with runtime (never overrides contract fields).
		if runtimeDetails != nil {
			enrichWithRuntime(result, runtimeDetails)
		}
	} else {
		// k8s-only service: no contract available.
		result = runtimeDetails
	}

	// Collect source list.
	var sources []string
	for _, cs := range contract {
		if _, err := cs.source.GetService(ctx, name); err == nil {
			sources = append(sources, cs.name)
		}
	}
	if runtimeDetails != nil {
		sources = append(sources, "k8s")
	}
	sort.Strings(sources)
	result.Sources = sources

	return result, nil
}

// enrichWithRuntime attaches k8s runtime fields to a contract-based service.
// It NEVER overrides contract content (interfaces, configuration, policy,
// dependencies, version, owner).
func enrichWithRuntime(contract *ServiceDetails, runtime *ServiceDetails) {
	// Phase: k8s is authoritative for runtime state.
	if runtime.Phase != PhaseUnknown && runtime.Phase != "" {
		contract.Phase = runtime.Phase
	}

	// Runtime-only struct fields: always set from k8s when present.
	enrichRuntimeFields(contract, runtime)

	// k8s-specific metadata that doesn't exist in contract.
	enrichRuntimeMetadata(contract, runtime)

	// Compliance: prefer k8s computed compliance (has conditions).
	if runtime.Compliance != nil {
		contract.Compliance = runtime.Compliance
	}
}

// enrichRuntimeFields copies runtime-only struct/slice fields from k8s.
func enrichRuntimeFields(contract *ServiceDetails, runtime *ServiceDetails) {
	if runtime.Runtime != nil {
		contract.Runtime = runtime.Runtime
	}
	if runtime.Resources != nil {
		contract.Resources = runtime.Resources
	}
	if runtime.Ports != nil {
		contract.Ports = runtime.Ports
	}
	if runtime.Validation != nil {
		contract.Validation = runtime.Validation
	}
	if runtime.Scaling != nil {
		contract.Scaling = runtime.Scaling
	}
	if runtime.ChecksSummary != nil {
		contract.ChecksSummary = runtime.ChecksSummary
	}
	if runtime.ObservedRuntime != nil {
		contract.ObservedRuntime = runtime.ObservedRuntime
	}
	if len(runtime.Endpoints) > 0 {
		contract.Endpoints = runtime.Endpoints
	}
	if len(runtime.Conditions) > 0 {
		contract.Conditions = runtime.Conditions
	}
	if len(runtime.Insights) > 0 {
		contract.Insights = runtime.Insights
	}
	if len(runtime.RuntimeDiff) > 0 {
		contract.RuntimeDiff = runtime.RuntimeDiff
	}
}

// enrichRuntimeMetadata copies k8s-specific string metadata fields.
func enrichRuntimeMetadata(contract *ServiceDetails, runtime *ServiceDetails) {
	if runtime.Namespace != "" {
		contract.Namespace = runtime.Namespace
	}
	if runtime.ResolvedRef != "" {
		contract.ResolvedRef = runtime.ResolvedRef
	}
	if runtime.CurrentRevision != "" {
		contract.CurrentRevision = runtime.CurrentRevision
	}
	if runtime.LastReconciledAt != "" {
		contract.LastReconciledAt = runtime.LastReconciledAt
	}
}

// resolverVersionSources governs the order in which sources are tried for GetVersions.
// k8s (PactoRevision CRDs) is most authoritative, then OCI, then local.
// Cache enrichment (hash, classification) is internal to OCISource.
var resolverVersionSources = []string{"k8s", "oci", "local"}

func (r *ResolvedSource) GetVersions(ctx context.Context, name string) ([]Version, error) {
	_, _, all := r.sources()

	// Merge versions from all sources, labeled by origin.
	// Later sources enrich earlier entries with missing fields.
	seen := make(map[string]int) // version string → index in merged
	var merged []Version

	for _, sourceType := range resolverVersionSources {
		ds, ok := all[sourceType]
		if !ok {
			continue
		}
		versions, err := ds.GetVersions(ctx, name)
		if err != nil || len(versions) == 0 {
			continue
		}
		for _, v := range versions {
			if idx, exists := seen[v.Version]; exists {
				enrichVersion(&merged[idx], &v)
				continue
			}
			seen[v.Version] = len(merged)
			v.Source = sourceType
			merged = append(merged, v)
		}
	}

	if len(merged) == 0 {
		return nil, fmt.Errorf("no version history available for %q", name)
	}

	// Sort descending by semver.
	sort.Slice(merged, func(i, j int) bool {
		return compareSemverDesc(merged[i].Version, merged[j].Version)
	})

	return merged, nil
}

// enrichVersion fills empty fields in dst with non-empty values from src.
func enrichVersion(dst, src *Version) {
	if dst.ContractHash == "" && src.ContractHash != "" {
		dst.ContractHash = src.ContractHash
	}
	if dst.CreatedAt == nil && src.CreatedAt != nil {
		dst.CreatedAt = src.CreatedAt
	}
	if dst.Classification == "" && src.Classification != "" {
		dst.Classification = src.Classification
	}
	if dst.Ref == "" && src.Ref != "" {
		dst.Ref = src.Ref
	}
}

func (r *ResolvedSource) GetDiff(ctx context.Context, from, to Ref) (*DiffResult, error) {
	_, _, all := r.sources()

	// Diff only works with contract bundle sources (not k8s).
	// Route to explicit source if specified.
	if from.Source != "" {
		if ds, ok := all[from.Source]; ok {
			return ds.GetDiff(ctx, from, to)
		}
	}

	// Try contract sources in order: oci first (has versioned bundles), then local.
	for _, sourceType := range []string{"oci", "local"} {
		ds, ok := all[sourceType]
		if !ok {
			continue
		}
		result, err := ds.GetDiff(ctx, from, to)
		if err == nil {
			return result, nil
		}
	}

	return nil, fmt.Errorf("diff requires contract bundle data (not available for %q)", from.Name)
}

// GetAggregated returns the per-source breakdown for the debug/sources endpoint.
func (r *ResolvedSource) GetAggregated(ctx context.Context, name string) (*AggregatedService, error) {
	contract, runtime, _ := r.sources()

	merged, err := r.GetService(ctx, name)
	if err != nil {
		return nil, err
	}

	agg := &AggregatedService{Name: name, Merged: merged}

	for _, cs := range contract {
		d, err := cs.source.GetService(ctx, name)
		if err == nil && d != nil {
			agg.Sources = append(agg.Sources, ServiceSourceData{SourceType: cs.name, Service: d})
		}
	}
	if runtime != nil {
		d, err := runtime.source.GetService(ctx, name)
		if err == nil && d != nil {
			agg.Sources = append(agg.Sources, ServiceSourceData{SourceType: "k8s", Service: d})
		}
	}

	return agg, nil
}

// SourceTypes returns the list of active source types.
func (r *ResolvedSource) SourceTypes() []string {
	_, _, all := r.sources()
	types := make([]string, 0, len(all))
	for st := range all {
		types = append(types, st)
	}
	sort.Strings(types)
	return types
}

// BuildResolvedSource creates a ResolvedSource from a map of cached data sources.
// It automatically separates contract sources (local, oci) from runtime (k8s).
func BuildResolvedSource(sources map[string]DataSource) *ResolvedSource {
	var contractSources []namedContractSource
	var runtimeSource DataSource

	// Contract sources in priority order: local first, then oci.
	for _, name := range []string{"local", "oci"} {
		if ds, ok := sources[name]; ok {
			contractSources = append(contractSources, namedContractSource{name: name, source: ds})
		}
	}

	if ds, ok := sources["k8s"]; ok {
		runtimeSource = ds
	}

	return NewResolvedSource(contractSources, runtimeSource, sources)
}

// isHigherContractPriority returns true if newSource has higher priority than current.
// local > oci (explicit dev intent wins).
func isHigherContractPriority(newSource, current string) bool {
	priority := map[string]int{"local": 0, "oci": 1}
	np, nok := priority[newSource]
	cp, cok := priority[current]
	if !nok {
		return false
	}
	if !cok {
		return true
	}
	return np < cp
}
