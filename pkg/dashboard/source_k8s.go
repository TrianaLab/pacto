package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// K8sSource implements DataSource by reading Pacto CRD status from a Kubernetes cluster.
// It uses k8s.io/client-go to communicate with the Kubernetes API server.
type K8sSource struct {
	client       K8sClient
	namespace    string
	resourceName string // CRD resource name, e.g. "pactos" (discovered dynamically)

	// listCache caches the result of listPactos for a short window to avoid
	// repeated API calls when buildServiceIndex calls GetService N times.
	listMu    sync.Mutex
	listCache []pactoResource
	listErr   error
	listAt    time.Time
}

// NewK8sSource creates a data source backed by Kubernetes CRDs.
// namespace may be empty to use all namespaces.
// resourceName is the CRD resource name (e.g. "pactos"), discovered dynamically.
func NewK8sSource(client K8sClient, namespace, resourceName string) *K8sSource {
	if resourceName == "" {
		resourceName = "pactos"
	}
	return &K8sSource{client: client, namespace: namespace, resourceName: resourceName}
}

// pactoResource represents the minimal structure of a Pacto CRD.
type pactoResource struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Status pactoStatus `json:"status"`
}

// pactoStatus maps the operator's PactoStatus to a JSON-parseable struct.
// Fields mirror the operator's v1alpha1.PactoStatus.
// Slice fields that the CRD may emit as a single object use json.RawMessage
// so we can handle both `{...}` (single) and `[{...}]` (array) formats.
type pactoStatus struct {
	Phase              string                   `json:"phase"`
	ContractVersion    string                   `json:"contractVersion"`
	Contract           *k8sContractInfo         `json:"contract,omitempty"`
	Validation         *k8sValidation           `json:"validation,omitempty"`
	Interfaces         flexSlice[k8sInterface]  `json:"interfaces,omitempty"`
	Configuration      *k8sConfig               `json:"configuration,omitempty"`
	Policy             *k8sPolicy               `json:"policy,omitempty"`
	Dependencies       flexSlice[k8sDependency] `json:"dependencies,omitempty"`
	Runtime            *k8sRuntime              `json:"runtime,omitempty"`
	Scaling            *k8sScaling              `json:"scaling,omitempty"`
	Resources          *k8sResources            `json:"resources,omitempty"`
	Ports              *k8sPorts                `json:"ports,omitempty"`
	Metadata           map[string]string        `json:"metadata,omitempty"`
	Summary            *k8sSummary              `json:"summary,omitempty"`
	Conditions         flexSlice[k8sCondition]  `json:"conditions,omitempty"`
	Endpoints          flexSlice[k8sEndpoint]   `json:"endpoints,omitempty"`
	Insights           flexSlice[k8sInsight]    `json:"insights,omitempty"`
	ObservedRuntime    *k8sObservedRuntime      `json:"observedRuntime,omitempty"`
	LastReconciledAt   string                   `json:"lastReconciledAt,omitempty"`
	ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
}

// flexSlice unmarshals a JSON value that may be either a single object or an
// array of objects. This handles CRD status fields whose shape varies depending
// on the number of entries the operator wrote.
type flexSlice[T any] []T

func (f *flexSlice[T]) UnmarshalJSON(data []byte) error {
	// Try array first (common case).
	var arr []T
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = arr
		return nil
	}
	// Fall back to single object.
	var single T
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*f = []T{single}
	return nil
}

type k8sContractInfo struct {
	ServiceName     string `json:"serviceName"`
	Version         string `json:"version"`
	Owner           string `json:"owner"`
	ImageRef        string `json:"imageRef"`
	ResolvedRef     string `json:"resolvedRef"`
	CurrentRevision string `json:"currentRevision,omitempty"`
}

type k8sValidation struct {
	Valid    bool       `json:"valid"`
	Errors   []k8sIssue `json:"errors,omitempty"`
	Warnings []k8sIssue `json:"warnings,omitempty"`
}

type k8sIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type k8sInterface struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Port            *int   `json:"port,omitempty"`
	Visibility      string `json:"visibility"`
	HasContractFile bool   `json:"hasContractFile"`
}

type k8sConfig struct {
	HasSchema  bool     `json:"hasSchema"`
	Ref        string   `json:"ref,omitempty"`
	ValueKeys  []string `json:"valueKeys,omitempty"`
	SecretKeys []string `json:"secretKeys,omitempty"`
}

type k8sPolicy struct {
	HasSchema bool   `json:"hasSchema"`
	Schema    string `json:"schema,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

type k8sDependency struct {
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
}

type k8sRuntime struct {
	Workload                string `json:"workload"`
	StateType               string `json:"stateType"`
	PersistenceScope        string `json:"persistenceScope"`
	PersistenceDurability   string `json:"persistenceDurability"`
	DataCriticality         string `json:"dataCriticality"`
	UpgradeStrategy         string `json:"upgradeStrategy"`
	GracefulShutdownSeconds *int   `json:"gracefulShutdownSeconds,omitempty"`
	HealthInterface         string `json:"healthInterface"`
	HealthPath              string `json:"healthPath"`
	MetricsInterface        string `json:"metricsInterface"`
	MetricsPath             string `json:"metricsPath"`
}

type k8sScaling struct {
	Replicas *int `json:"replicas,omitempty"`
	Min      *int `json:"min,omitempty"`
	Max      *int `json:"max,omitempty"`
}

type k8sResources struct {
	Service  *k8sResourceStatus `json:"service,omitempty"`
	Workload *k8sResourceStatus `json:"workload,omitempty"`
}

type k8sResourceStatus struct {
	Exists bool `json:"exists"`
}

type k8sPorts struct {
	Expected   []int `json:"expected,omitempty"`
	Observed   []int `json:"observed,omitempty"`
	Missing    []int `json:"missing,omitempty"`
	Unexpected []int `json:"unexpected,omitempty"`
}

type k8sSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type k8sCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	Severity           string `json:"severity,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

type k8sObservedRuntime struct {
	WorkloadKind                   string   `json:"workloadKind,omitempty"`
	DeploymentStrategy             string   `json:"deploymentStrategy,omitempty"`
	PodManagementPolicy            string   `json:"podManagementPolicy,omitempty"`
	TerminationGracePeriodSeconds  *int     `json:"terminationGracePeriodSeconds,omitempty"`
	ContainerImages                []string `json:"containerImages,omitempty"`
	HasPVC                         *bool    `json:"hasPVC,omitempty"`
	HasEmptyDir                    *bool    `json:"hasEmptyDir,omitempty"`
	HealthProbeInitialDelaySeconds *int     `json:"healthProbeInitialDelaySeconds,omitempty"`
}

type k8sEndpoint struct {
	Interface  string `json:"interface"`
	Type       string `json:"type,omitempty"` // "health", "metrics", or empty
	URL        string `json:"url,omitempty"`
	Healthy    *bool  `json:"healthy,omitempty"`
	StatusCode *int   `json:"statusCode,omitempty"`
	LatencyMs  *int64 `json:"latencyMs,omitempty"`
	Error      string `json:"error,omitempty"`
	Message    string `json:"message,omitempty"`
}

type k8sInsight struct {
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

func (s *K8sSource) ListServices(ctx context.Context) ([]Service, error) {
	resources, err := s.listPactos(ctx)
	if err != nil {
		return nil, err
	}

	var services []Service
	for _, r := range resources {
		svc := serviceFromK8sStatus(r)
		services = append(services, svc)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services, nil
}

func (s *K8sSource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	r, err := s.getPacto(ctx, name)
	if err != nil {
		return nil, err
	}
	return serviceDetailsFromK8sStatus(r), nil
}

func (s *K8sSource) GetVersions(_ context.Context, _ string) ([]Version, error) {
	// K8s source only knows the current deployed version.
	// Version history would require PactoRevision listing, which is a future enhancement.
	return nil, fmt.Errorf("version history not yet supported for k8s source")
}

func (s *K8sSource) GetDiff(_ context.Context, _, _ Ref) (*DiffResult, error) {
	return nil, fmt.Errorf("diff not yet supported for k8s source; use OCI or local source")
}

// listCacheTTL controls how long a listPactos result is reused.
// This prevents the N×API-call explosion when buildServiceIndex calls
// GetService for every service in rapid succession.
const listCacheTTL = 3 * time.Second

func (s *K8sSource) listPactos(ctx context.Context) ([]pactoResource, error) {
	s.listMu.Lock()
	if !s.listAt.IsZero() && time.Since(s.listAt) < listCacheTTL {
		items, err := s.listCache, s.listErr
		s.listMu.Unlock()
		return items, err
	}
	s.listMu.Unlock()

	out, err := s.client.ListJSON(ctx, s.resourceName, s.namespace)
	if err != nil {
		listErr := fmt.Errorf("listing %s: %w", s.resourceName, err)
		s.setListCache(nil, listErr)
		return nil, listErr
	}

	var list struct {
		Items []pactoResource `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		parseErr := fmt.Errorf("parsing API response: %w", err)
		s.setListCache(nil, parseErr)
		return nil, parseErr
	}
	s.setListCache(list.Items, nil)
	return list.Items, nil
}

func (s *K8sSource) setListCache(items []pactoResource, err error) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	s.listCache = items
	s.listErr = err
	s.listAt = time.Now()
}

func (s *K8sSource) getPacto(ctx context.Context, name string) (*pactoResource, error) {
	if s.namespace != "" {
		// Direct get with known namespace.
		out, err := s.client.GetJSON(ctx, s.resourceName, s.namespace, name)
		if err != nil {
			return nil, fmt.Errorf("getting %s %s: %w", s.resourceName, name, err)
		}
		var r pactoResource
		if err := json.Unmarshal(out, &r); err != nil {
			return nil, fmt.Errorf("parsing API response: %w", err)
		}
		return &r, nil
	}

	// All-namespaces mode: list all and find by name.
	resources, err := s.listPactos(ctx)
	if err != nil {
		return nil, err
	}
	for i := range resources {
		r := &resources[i]
		svcName := r.Metadata.Name
		if r.Status.Contract != nil && r.Status.Contract.ServiceName != "" {
			svcName = r.Status.Contract.ServiceName
		}
		if svcName == name || r.Metadata.Name == name {
			return r, nil
		}
	}
	return nil, fmt.Errorf("pacto resource %q not found", name)
}

// Mapping: operator status -> dashboard model

func serviceFromK8sStatus(r pactoResource) Service {
	svc := Service{
		Name:   r.Metadata.Name,
		Phase:  NormalizePhase(Phase(r.Status.Phase)),
		Source: "k8s",
	}
	if r.Status.Contract != nil {
		svc.Name = r.Status.Contract.ServiceName
		svc.Version = r.Status.Contract.Version
		svc.Owner = r.Status.Contract.Owner
	}
	if r.Status.ContractVersion != "" {
		svc.Version = r.Status.ContractVersion
	}
	return svc
}

func serviceDetailsFromK8sStatus(r *pactoResource) *ServiceDetails {
	svc := &ServiceDetails{
		Service: serviceFromK8sStatus(*r),
	}

	svc.Namespace = r.Metadata.Namespace

	if r.Status.Contract != nil {
		svc.ImageRef = r.Status.Contract.ImageRef
		svc.ResolvedRef = r.Status.Contract.ResolvedRef
		svc.CurrentRevision = r.Status.Contract.CurrentRevision
	}

	svc.Metadata = r.Status.Metadata

	if r.Status.LastReconciledAt != "" {
		svc.LastReconciledAt = timeAgoFromRFC3339(r.Status.LastReconciledAt)
	}

	// Interfaces
	for _, i := range r.Status.Interfaces {
		svc.Interfaces = append(svc.Interfaces, InterfaceInfo{
			Name:            i.Name,
			Type:            i.Type,
			Port:            i.Port,
			Visibility:      i.Visibility,
			HasContractFile: i.HasContractFile,
		})
	}

	// Configuration
	if r.Status.Configuration != nil {
		svc.Configuration = &ConfigurationInfo{
			HasSchema:  r.Status.Configuration.HasSchema,
			Ref:        r.Status.Configuration.Ref,
			ValueKeys:  r.Status.Configuration.ValueKeys,
			SecretKeys: r.Status.Configuration.SecretKeys,
		}
	}

	// Policy
	if r.Status.Policy != nil {
		svc.Policy = &PolicyInfo{
			HasSchema: r.Status.Policy.HasSchema,
			Schema:    r.Status.Policy.Schema,
			Ref:       r.Status.Policy.Ref,
		}
	}

	// Dependencies
	for _, d := range r.Status.Dependencies {
		svc.Dependencies = append(svc.Dependencies, DependencyInfo{
			Name:          extractServiceNameFromRef(d.Ref),
			Ref:           d.Ref,
			Required:      d.Required,
			Compatibility: d.Compatibility,
		})
	}

	// Runtime
	if r.Status.Runtime != nil {
		svc.Runtime = &RuntimeInfo{
			Workload:                r.Status.Runtime.Workload,
			StateType:               r.Status.Runtime.StateType,
			PersistenceScope:        r.Status.Runtime.PersistenceScope,
			PersistenceDurability:   r.Status.Runtime.PersistenceDurability,
			DataCriticality:         r.Status.Runtime.DataCriticality,
			UpgradeStrategy:         r.Status.Runtime.UpgradeStrategy,
			GracefulShutdownSeconds: r.Status.Runtime.GracefulShutdownSeconds,
			HealthInterface:         r.Status.Runtime.HealthInterface,
			HealthPath:              r.Status.Runtime.HealthPath,
			MetricsInterface:        r.Status.Runtime.MetricsInterface,
			MetricsPath:             r.Status.Runtime.MetricsPath,
		}
	}

	// Scaling
	if r.Status.Scaling != nil {
		svc.Scaling = &ScalingInfo{
			Replicas: r.Status.Scaling.Replicas,
			Min:      r.Status.Scaling.Min,
			Max:      r.Status.Scaling.Max,
		}
	}

	svc.Validation = validationFromK8s(r.Status.Validation)
	svc.Resources = resourcesFromK8s(r.Status.Resources)
	svc.Ports = portsFromK8s(r.Status.Ports)
	svc.Conditions = conditionsFromK8s(r.Status.Conditions)
	svc.Endpoints = endpointsFromK8s(r.Status.Endpoints)
	svc.Insights = insightsFromK8s(r.Status.Insights)
	svc.ObservedRuntime = observedRuntimeFromK8s(r.Status.ObservedRuntime)
	if r.Status.Summary != nil {
		svc.ChecksSummary = &ChecksSummary{
			Total:  r.Status.Summary.Total,
			Passed: r.Status.Summary.Passed,
			Failed: r.Status.Summary.Failed,
		}
	}

	// Compute compliance from phase and conditions.
	svc.Compliance = ComputeCompliance(svc.Phase, svc.Conditions)

	// Compute runtime diff if both contract runtime and observed runtime are available.
	svc.RuntimeDiff = ComputeRuntimeDiff(svc.Runtime, svc.ObservedRuntime)

	return svc
}

func validationFromK8s(v *k8sValidation) *ValidationInfo {
	if v == nil {
		return nil
	}
	vi := &ValidationInfo{Valid: v.Valid}
	for _, e := range v.Errors {
		vi.Errors = append(vi.Errors, ValidationIssue(e))
	}
	for _, w := range v.Warnings {
		vi.Warnings = append(vi.Warnings, ValidationIssue(w))
	}
	return vi
}

func resourcesFromK8s(res *k8sResources) *ResourcesInfo {
	if res == nil {
		return nil
	}
	ri := &ResourcesInfo{}
	if res.Service != nil {
		v := res.Service.Exists
		ri.ServiceExists = &v
	}
	if res.Workload != nil {
		v := res.Workload.Exists
		ri.WorkloadExists = &v
	}
	return ri
}

func portsFromK8s(p *k8sPorts) *PortsInfo {
	if p == nil {
		return nil
	}
	return &PortsInfo{Expected: p.Expected, Observed: p.Observed, Missing: p.Missing, Unexpected: p.Unexpected}
}

func conditionsFromK8s(conditions flexSlice[k8sCondition]) []Condition {
	var out []Condition
	for _, c := range conditions {
		cond := Condition{Type: c.Type, Status: c.Status, Reason: c.Reason, Message: c.Message, Severity: c.Severity}
		if c.LastTransitionTime != "" {
			cond.LastTransitionAgo = timeAgoFromRFC3339(c.LastTransitionTime)
		}
		out = append(out, cond)
	}
	return out
}

func observedRuntimeFromK8s(obs *k8sObservedRuntime) *ObservedRuntime {
	if obs == nil {
		return nil
	}
	return &ObservedRuntime{
		WorkloadKind:                  obs.WorkloadKind,
		DeploymentStrategy:            obs.DeploymentStrategy,
		PodManagementPolicy:           obs.PodManagementPolicy,
		TerminationGracePeriodSeconds: obs.TerminationGracePeriodSeconds,
		ContainerImages:               obs.ContainerImages,
		HasPVC:                        obs.HasPVC,
		HasEmptyDir:                   obs.HasEmptyDir,
		HealthProbeInitialDelay:       obs.HealthProbeInitialDelaySeconds,
	}
}

func endpointsFromK8s(endpoints flexSlice[k8sEndpoint]) []EndpointStatus {
	var out []EndpointStatus
	for _, ep := range endpoints {
		out = append(out, EndpointStatus(ep))
	}
	return out
}

func insightsFromK8s(insights []k8sInsight) []Insight {
	var out []Insight
	for _, ins := range insights {
		out = append(out, Insight(ins))
	}
	return out
}

// timeAgoFromRFC3339 parses an RFC3339 timestamp and returns a human-readable "X ago" string.
func timeAgoFromRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
