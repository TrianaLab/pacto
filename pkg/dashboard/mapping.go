package dashboard

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/diff"
	"github.com/trianalab/pacto/pkg/doc"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/readiness"
	"github.com/trianalab/pacto/pkg/schemax"
	"github.com/trianalab/pacto/pkg/validation"
)

// timeNow is the clock used to derive readiness freshness. It is a variable so
// tests can pin "now" for deterministic readiness status.
var timeNow = time.Now

// ServiceFromContract builds a Service summary from a parsed contract.
func ServiceFromContract(c *contract.Contract, source string) Service {
	return Service{
		Name:           c.Service.Name,
		Version:        c.Service.Version,
		Owner:          c.Service.Owner,
		ContractStatus: StatusUnknown,
		Source:         source,
	}
}

// contractStatusFromBundle computes the ContractStatus from a bundle without
// building full details. Used by ListServices to avoid expensive
// validation/parsing just for the list view.
func contractStatusFromBundle(bundle *contract.Bundle) ContractStatus {
	if bundle.RawYAML == nil {
		return StatusUnknown
	}
	result := validation.Validate(bundle.Contract, bundle.RawYAML, bundle.FS)
	if result.IsValid() {
		return StatusCompliant
	}
	return StatusNonCompliant
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
	svc.Configurations = configsFromContract(c, bundle.FS)
	svc.Dependencies = depsFromContract(c)
	svc.Runtime = runtimeFromContract(c)
	svc.Scaling = scalingFromContract(c)
	svc.Policies = policiesFromContract(c, bundle.FS)
	svc.Docs = docsFromContract(bundle.FS)
	svc.Readiness = readinessFromContract(c, docPathSet(svc.Docs))
	svc.Metadata = metadataFromContract(c)

	// Validation
	if bundle.RawYAML != nil {
		result := validation.Validate(c, bundle.RawYAML, bundle.FS)
		svc.Validation = validationInfoFromResult(result)
		if result.IsValid() {
			svc.ContractStatus = StatusCompliant
		} else {
			svc.ContractStatus = StatusNonCompliant
		}
	}

	// Compute compliance for non-k8s sources (no conditions available).
	svc.Compliance = ComputeCompliance(svc.ContractStatus, svc.Conditions)

	// Apply the embedded lockfile (pacto.lock) when the bundle FS carries one.
	// Since the lock now ships inside the bundle for every source (local, OCI,
	// cache), this single read surfaces pins and lights up dependency drift
	// cluster-wide. A nil FS or absent/malformed lock leaves svc untouched.
	if l, err := lockFromFS(bundle.FS); err == nil {
		ApplyLock(svc, l)
	}

	return svc
}

// readinessFromContract derives the readiness assessment from the contract's
// declared readiness section as of now. It returns nil when no readiness is
// declared, so the dashboard treats readiness as an optional dimension. A check
// whose evidence is the path of an in-bundle doc (a key in docPaths) gets DocPath
// set so the UI can render that doc inline.
func readinessFromContract(c *contract.Contract, docPaths map[string]bool) *ReadinessInfo {
	eval := readiness.Evaluate(c.Readiness, timeNow())
	if eval == nil {
		return nil
	}
	info := &ReadinessInfo{
		Score:         eval.Score,
		MinScore:      eval.MinScore,
		TotalWeight:   eval.TotalWeight,
		EarnedWeight:  eval.EarnedWeight,
		PartialCredit: eval.PartialCredit,
		Passing:       eval.Passing,
		Expires:       eval.Expires,
		Expired:       eval.Expired,
		DaysRemaining: eval.DaysRemaining,
		DoneCount:     eval.DoneCount,
		PartialCount:  eval.PartialCount,
		NotDoneCount:  eval.NotDoneCount,
		DeferredCount: eval.DeferredCount,
	}
	for _, ch := range eval.Checks {
		ci := ReadinessCheckInfo{
			ID:           ch.ID,
			Type:         ch.Type,
			Category:     ch.Category,
			Status:       ch.Status,
			Evidence:     ch.Evidence,
			Description:  ch.Description,
			Weight:       ch.Weight,
			EarnedWeight: ch.EarnedWeight,
			Excluded:     ch.Excluded,
		}
		if docPaths[ch.Evidence] {
			ci.DocPath = ch.Evidence
		}
		info.Checks = append(info.Checks, ci)
	}
	// Revision history is authored, not derived: copy it straight from the contract.
	for _, rev := range c.Readiness.History {
		info.Revisions = append(info.Revisions, ReadinessRevisionInfo{
			Date:        rev.Date,
			Version:     rev.Version,
			Author:      rev.Author,
			Description: rev.Description,
		})
	}
	return info
}

// Caps on in-bundle docs surfaced to the dashboard. Vars (not consts) so tests
// can shrink them. The dashboard is a local read-only tool, so eager inlining
// with these caps is acceptable.
var (
	maxDocBytes      = 256 * 1024  // per-doc cap before truncation
	maxTotalDocBytes = 1024 * 1024 // total cap across all docs
	maxDocCount      = 50          // safety cap on number of docs
)

const docsDir = "docs"

// docsFromContract reads the bundle's docs/**/*.md files (sorted by path),
// applying per-doc, total, and count caps. Missing docs/ or an unreadable file
// is skipped rather than failing the whole service.
func docsFromContract(fsys fs.FS) []DocInfo {
	if fsys == nil {
		return nil
	}
	var docs []DocInfo
	total := 0
	_ = fs.WalkDir(fsys, docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // docs/ absent or an unreadable subtree: skip silently
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if len(docs) >= maxDocCount || total >= maxTotalDocBytes {
			return fs.SkipAll
		}
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return nil // skip unreadable file
		}
		truncated := false
		if len(data) > maxDocBytes {
			data = data[:maxDocBytes]
			truncated = true
		}
		if total+len(data) > maxTotalDocBytes {
			data = data[:maxTotalDocBytes-total]
			truncated = true
		}
		total += len(data)
		content := string(data)
		docs = append(docs, DocInfo{
			Path:      p,
			Title:     docTitle(content, p),
			Content:   content,
			Truncated: truncated,
		})
		return nil
	})
	return docs
}

// docTitle returns the first Markdown H1 in content, or a humanized filename.
func docTitle(content, p string) string {
	for _, line := range strings.Split(content, "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	name := strings.TrimSuffix(path.Base(p), path.Ext(p))
	name = strings.ReplaceAll(name, "-", " ")
	return strings.ReplaceAll(name, "_", " ")
}

// docPathSet builds a lookup of the doc paths present in the bundle.
func docPathSet(docs []DocInfo) map[string]bool {
	if len(docs) == 0 {
		return nil
	}
	set := make(map[string]bool, len(docs))
	for _, d := range docs {
		set[d.Path] = true
	}
	return set
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

func configsFromContract(c *contract.Contract, fsys fs.FS) []ConfigurationInfo {
	if len(c.Configurations) == 0 {
		return nil
	}
	var out []ConfigurationInfo
	for _, cfg := range c.Configurations {
		ci := ConfigurationInfo{
			Name:      cfg.Name,
			HasSchema: cfg.Schema != "",
			Schema:    cfg.Schema,
			Ref:       cfg.Ref,
		}
		if len(cfg.Values) > 0 {
			ci.Values = schemax.Values(cfg.Values)
			for k := range cfg.Values {
				ci.ValueKeys = append(ci.ValueKeys, k)
			}
			sort.Strings(ci.ValueKeys)
		} else if cfg.Schema != "" && fsys != nil {
			ci.Values = extractSchemaProperties(fsys, cfg.Schema)
		}
		out = append(out, ci)
	}
	return out
}

func depsFromContract(c *contract.Contract) []DependencyInfo {
	var out []DependencyInfo
	for _, dep := range c.Dependencies {
		out = append(out, DependencyInfo{
			Name:          dep.Name,
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

func policiesFromContract(c *contract.Contract, fsys fs.FS) []PolicyInfo {
	// If the contract declares policies explicitly, use them.
	if len(c.Policies) > 0 {
		var out []PolicyInfo
		for _, pol := range c.Policies {
			pi := PolicyInfo{
				Name:      pol.Name,
				HasSchema: pol.Schema != "",
				Schema:    pol.Schema,
				Ref:       pol.Ref,
			}
			if pol.Ref != "" && fsys != nil {
				enrichPolicyFromFile(&pi, fsys, pol.Ref)
			}
			if len(pi.Values) == 0 && pol.Schema != "" && fsys != nil {
				pi.Values = extractSchemaProperties(fsys, pol.Schema)
			}
			if fsys != nil {
				path := pol.Schema
				if path == "" {
					path = pol.Ref
				}
				if path != "" {
					pi.Title, pi.Description = extractSchemaMeta(fsys, path)
				}
			}
			out = append(out, pi)
		}
		return out
	}

	// Auto-detect: bundle ships policy/schema.json but contract has no policies declared.
	if fsys == nil {
		return nil
	}
	data, err := fs.ReadFile(fsys, validation.PolicySchemaPath)
	if err != nil {
		return nil
	}
	title, desc := extractSchemaMeta(fsys, validation.PolicySchemaPath)
	pi := PolicyInfo{
		Name:        "default",
		HasSchema:   true,
		Schema:      validation.PolicySchemaPath,
		Title:       title,
		Description: desc,
		Content:     truncateContent(string(data)),
		Values:      extractSchemaProperties(fsys, validation.PolicySchemaPath),
	}
	if len(pi.Values) == 0 {
		pi.Values = parseContentAsValues(data, validation.PolicySchemaPath)
	}
	return []PolicyInfo{pi}
}

func enrichPolicyFromFile(pi *PolicyInfo, fsys fs.FS, path string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return
	}
	pi.Content = truncateContent(string(data))
	pi.Values = extractSchemaProperties(fsys, path)
	if len(pi.Values) == 0 {
		pi.Values = parseContentAsValues(data, path)
	}
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

// parseContentAsValues tries to parse raw file content as YAML/JSON key-value pairs.
func parseContentAsValues(data []byte, path string) []ConfigValue {
	// Reuse the OpenAPI spec parser's unmarshal logic: JSON for .json, YAML otherwise.
	spec, err := doc.UnmarshalSpec(data, path)
	if err != nil || len(spec) == 0 {
		return nil
	}
	return schemax.Values(spec)
}

// extractSchemaMeta reads title and description from a JSON Schema file in the
// bundle FS. Extraction itself lives in pkg/schemax so the operator produces
// identical results from the same bundle.
func extractSchemaMeta(fsys fs.FS, path string) (title, description string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", ""
	}
	return schemax.Meta(data, path)
}

// extractSchemaProperties reads a JSON Schema file from the bundle FS and
// extracts its flattened properties (shared with the operator via pkg/schemax).
func extractSchemaProperties(fsys fs.FS, path string) []ConfigValue {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	return schemax.Properties(data, path)
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
