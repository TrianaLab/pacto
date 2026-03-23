package dashboard

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/diff"
	"github.com/trianalab/pacto/pkg/doc"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/validation"
)

// ServiceFromContract builds a Service summary from a parsed contract.
func ServiceFromContract(c *contract.Contract, source string) Service {
	return Service{
		Name:    c.Service.Name,
		Version: c.Service.Version,
		Owner:   c.Service.Owner,
		Phase:   PhaseUnknown,
		Source:  source,
	}
}

// phaseFromBundle computes the Phase from a bundle without building full details.
// Used by ListServices to avoid expensive validation/parsing just for the list view.
func phaseFromBundle(bundle *contract.Bundle) Phase {
	if bundle.RawYAML == nil {
		return PhaseUnknown
	}
	result := validation.Validate(bundle.Contract, bundle.RawYAML, bundle.FS)
	if result.IsValid() {
		return PhaseHealthy
	}
	return PhaseInvalid
}

// ServiceDetailsFromBundle builds full ServiceDetails from a contract bundle.
func ServiceDetailsFromBundle(bundle *contract.Bundle, source string) *ServiceDetails {
	c := bundle.Contract

	svc := &ServiceDetails{
		Service: ServiceFromContract(c, source),
	}

	if c.Service.Image != nil {
		svc.ImageRef = c.Service.Image.Ref
	}
	if c.Service.Chart != nil {
		svc.ChartRef = c.Service.Chart.Ref
	}

	svc.Interfaces = interfacesFromContract(c, bundle.FS)
	svc.Configuration = configFromContract(c, bundle.FS)
	svc.Dependencies = depsFromContract(c)
	svc.Runtime = runtimeFromContract(c)
	svc.Scaling = scalingFromContract(c)
	svc.Policy = policyFromContract(c, bundle.FS)
	svc.Metadata = metadataFromContract(c)

	// Validation
	if bundle.RawYAML != nil {
		result := validation.Validate(c, bundle.RawYAML, bundle.FS)
		svc.Validation = validationInfoFromResult(result)
		if result.IsValid() {
			svc.Phase = PhaseHealthy
		} else {
			svc.Phase = PhaseInvalid
		}
	}

	return svc
}

func interfacesFromContract(c *contract.Contract, fsys fs.FS) []InterfaceInfo {
	var out []InterfaceInfo
	for _, iface := range c.Interfaces {
		info := InterfaceInfo{
			Name:            iface.Name,
			Type:            iface.Type,
			Port:            iface.Port,
			Visibility:      iface.Visibility,
			HasContractFile: iface.Contract != "",
			ContractFile:    iface.Contract,
		}
		if iface.Contract != "" && fsys != nil {
			endpoints, err := doc.ReadOpenAPIEndpoints(fsys, iface.Contract)
			if err == nil && len(endpoints) > 0 {
				for _, ep := range endpoints {
					info.Endpoints = append(info.Endpoints, InterfaceEndpoint{
						Method:  strings.ToUpper(ep.Method),
						Path:    ep.Path,
						Summary: ep.Summary,
					})
				}
			} else {
				if data, readErr := fs.ReadFile(fsys, iface.Contract); readErr == nil {
					info.ContractContent = truncateContent(string(data))
				}
			}
		}
		out = append(out, info)
	}
	return out
}

func configFromContract(c *contract.Contract, fsys fs.FS) *ConfigurationInfo {
	if c.Configuration == nil {
		return nil
	}
	ci := &ConfigurationInfo{
		HasSchema: c.Configuration.Schema != "",
		Schema:    c.Configuration.Schema,
		Ref:       c.Configuration.Ref,
	}
	if len(c.Configuration.Values) > 0 {
		ci.Values = flattenValues(c.Configuration.Values)
		for k := range c.Configuration.Values {
			ci.ValueKeys = append(ci.ValueKeys, k)
		}
	} else if c.Configuration.Schema != "" && fsys != nil {
		ci.Values = extractSchemaProperties(fsys, c.Configuration.Schema)
	}
	return ci
}

func depsFromContract(c *contract.Contract) []DependencyInfo {
	var out []DependencyInfo
	for _, dep := range c.Dependencies {
		out = append(out, DependencyInfo{
			Name:          extractServiceNameFromRef(dep.Ref),
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
		})
	}
	return out
}

func runtimeFromContract(c *contract.Contract) *RuntimeInfo {
	if c.Runtime == nil {
		return nil
	}
	ri := &RuntimeInfo{
		Workload:              c.Runtime.Workload,
		StateType:             c.Runtime.State.Type,
		DataCriticality:       c.Runtime.State.DataCriticality,
		PersistenceScope:      c.Runtime.State.Persistence.Scope,
		PersistenceDurability: c.Runtime.State.Persistence.Durability,
	}
	if c.Runtime.Lifecycle != nil {
		ri.UpgradeStrategy = c.Runtime.Lifecycle.UpgradeStrategy
		ri.GracefulShutdownSeconds = c.Runtime.Lifecycle.GracefulShutdownSeconds
	}
	if c.Runtime.Health != nil {
		ri.HealthInterface = c.Runtime.Health.Interface
		ri.HealthPath = c.Runtime.Health.Path
	}
	if c.Runtime.Metrics != nil {
		ri.MetricsInterface = c.Runtime.Metrics.Interface
		ri.MetricsPath = c.Runtime.Metrics.Path
	}
	return ri
}

func scalingFromContract(c *contract.Contract) *ScalingInfo {
	if c.Scaling == nil {
		return nil
	}
	si := &ScalingInfo{Replicas: c.Scaling.Replicas}
	if c.Scaling.Min > 0 {
		v := c.Scaling.Min
		si.Min = &v
	}
	if c.Scaling.Max > 0 {
		v := c.Scaling.Max
		si.Max = &v
	}
	return si
}

func policyFromContract(c *contract.Contract, fsys fs.FS) *PolicyInfo {
	if c.Policy == nil {
		return nil
	}
	pi := &PolicyInfo{
		HasSchema: c.Policy.Schema != "",
		Schema:    c.Policy.Schema,
		Ref:       c.Policy.Ref,
	}
	if c.Policy.Ref != "" && fsys != nil {
		if data, err := fs.ReadFile(fsys, c.Policy.Ref); err == nil {
			pi.Content = truncateContent(string(data))
			pi.Values = extractSchemaProperties(fsys, c.Policy.Ref)
			if len(pi.Values) == 0 {
				pi.Values = parseContentAsValues(data, c.Policy.Ref)
			}
		}
	}
	if len(pi.Values) == 0 && c.Policy.Schema != "" && fsys != nil {
		pi.Values = extractSchemaProperties(fsys, c.Policy.Schema)
	}
	return pi
}

func metadataFromContract(c *contract.Contract) map[string]string {
	if len(c.Metadata) == 0 {
		return nil
	}
	m := make(map[string]string, len(c.Metadata))
	for k, v := range c.Metadata {
		if s, ok := v.(string); ok {
			m[k] = s
		}
	}
	return m
}

func truncateContent(content string) string {
	if len(content) > 10240 {
		return content[:10240] + "\n... (truncated)"
	}
	return content
}

func validationInfoFromResult(r validation.ValidationResult) *ValidationInfo {
	vi := &ValidationInfo{Valid: r.IsValid()}
	for _, e := range r.Errors {
		vi.Errors = append(vi.Errors, ValidationIssue{
			Code:    e.Code,
			Path:    e.Path,
			Message: e.Message,
		})
	}
	for _, w := range r.Warnings {
		vi.Warnings = append(vi.Warnings, ValidationIssue{
			Code:    w.Code,
			Path:    w.Path,
			Message: w.Message,
		})
	}
	return vi
}

// DiffResultFromEngine maps the diff engine's Result to the dashboard DiffResult.
func DiffResultFromEngine(from, to Ref, r *diff.Result) *DiffResult {
	dr := &DiffResult{
		From:           from,
		To:             to,
		Classification: r.Classification.String(),
	}
	for _, c := range r.Changes {
		dr.Changes = append(dr.Changes, DiffChange{
			Path:           c.Path,
			Type:           c.Type.String(),
			OldValue:       c.OldValue,
			NewValue:       c.NewValue,
			Classification: c.Classification.String(),
			Reason:         c.Reason,
		})
	}
	return dr
}

// graphFromResult maps the graph resolver's Result to the dashboard DependencyGraph.
func graphFromResult(r *graph.Result) *DependencyGraph {
	if r == nil || r.Root == nil {
		return nil
	}
	g := &DependencyGraph{
		Root:   mapGraphNode(r.Root),
		Cycles: r.Cycles,
	}
	for _, c := range r.Conflicts {
		g.Conflicts = append(g.Conflicts, fmt.Sprintf("%s: %v", c.Name, c.Versions))
	}
	return g
}

func mapGraphNode(n *graph.Node) *GraphNode {
	if n == nil {
		return nil
	}
	gn := &GraphNode{
		Name:    n.Name,
		Version: n.Version,
		Ref:     n.Ref,
	}
	for _, e := range n.Dependencies {
		ge := GraphEdge{
			Ref:           e.Ref,
			Required:      e.Required,
			Compatibility: e.Compatibility,
			Error:         e.Error,
			Shared:        e.Shared,
			Node:          mapGraphNode(e.Node),
		}
		gn.Dependencies = append(gn.Dependencies, ge)
	}
	return gn
}

// validateBundle runs full validation on a bundle and returns dashboard-model results.
func validateBundle(bundle *contract.Bundle) *ValidationInfo {
	if bundle.RawYAML == nil {
		return nil
	}
	r := validation.Validate(bundle.Contract, bundle.RawYAML, bundle.FS)
	return validationInfoFromResult(r)
}

// flattenValues converts a map[string]interface{} to sorted []ConfigValue entries.
func flattenValues(m map[string]interface{}) []ConfigValue {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	values := make([]ConfigValue, 0, len(m))
	for _, k := range keys {
		v := m[k]
		cv := ConfigValue{Key: k}
		switch val := v.(type) {
		case string:
			cv.Value = val
			cv.Type = "string"
		case float64:
			cv.Value = fmt.Sprintf("%g", val)
			cv.Type = "number"
		case int:
			cv.Value = fmt.Sprintf("%d", val)
			cv.Type = "number"
		case bool:
			cv.Value = fmt.Sprintf("%t", val)
			cv.Type = "boolean"
		case nil:
			cv.Value = "(any)"
			cv.Type = "any"
		default:
			cv.Value = fmt.Sprintf("%v", val)
			cv.Type = "object"
		}
		values = append(values, cv)
	}
	return values
}

// parseContentAsValues tries to parse raw file content as YAML/JSON key-value pairs.
func parseContentAsValues(data []byte, path string) []ConfigValue {
	// Reuse the OpenAPI spec parser's unmarshal logic: JSON for .json, YAML otherwise.
	spec, err := doc.UnmarshalSpec(data, path)
	if err != nil || len(spec) == 0 {
		return nil
	}
	return flattenValues(spec)
}

// extractSchemaProperties reads a JSON Schema file from the bundle FS and
// extracts top-level properties into ConfigValue entries. Nested objects are
// recursively flattened with dot-notation keys.
func extractSchemaProperties(fsys fs.FS, path string) []ConfigValue {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	spec, err := doc.UnmarshalSpec(data, path)
	if err != nil {
		return nil
	}
	propsRaw, ok := spec["properties"]
	if !ok {
		return nil
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil
	}
	var values []ConfigValue
	flattenSchemaProps("", props, &values)
	return values
}

// flattenSchemaProps recursively walks JSON Schema properties and produces
// ConfigValue entries. Nested objects use dot-notation (e.g. "cors.enabled").
func flattenSchemaProps(prefix string, props map[string]any, out *[]ConfigValue) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		propRaw, ok := props[k].(map[string]any)
		if !ok {
			continue
		}
		// If this property is an object with sub-properties, recurse.
		if subType, _ := propRaw["type"].(string); subType == "object" {
			if subPropsRaw, ok := propRaw["properties"]; ok {
				if subProps, ok := subPropsRaw.(map[string]any); ok {
					flattenSchemaProps(fullKey, subProps, out)
					continue
				}
			}
		}
		cv := ConfigValue{Key: fullKey}
		if t, ok := propRaw["type"].(string); ok {
			cv.Type = t
		}
		if def, ok := propRaw["default"]; ok {
			cv.Value = fmt.Sprintf("%v", def)
		} else {
			cv.Value = "(any)"
		}
		*out = append(*out, cv)
	}
}

// ComputeDiff runs the diff engine on two bundles and returns a dashboard DiffResult.
func ComputeDiff(from, to Ref, oldBundle, newBundle *contract.Bundle) *DiffResult {
	var oldFS, newFS fs.FS
	if oldBundle.FS != nil {
		oldFS = oldBundle.FS
	}
	if newBundle.FS != nil {
		newFS = newBundle.FS
	}
	r := diff.Compare(oldBundle.Contract, newBundle.Contract, oldFS, newFS)
	return DiffResultFromEngine(from, to, r)
}
