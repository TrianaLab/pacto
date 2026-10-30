/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/schemax"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// populateContractStatus extracts structured data from the parsed contract into status fields.
// Policy sources are surfaced as metadata only — the operator does not resolve or enforce
// ref-based policies. Local policy schemas bundled in the contract are enforced by
// validation.Validate() during contract loading.
func (r *PactoReconciler) populateContractStatus(pacto *pactov1alpha1.Pacto, lr *loader.LoadResult) {
	c := lr.Contract

	// Initialize slices so JSON output is [] instead of null when empty.
	pacto.Status.Configurations = []pactov1alpha1.ConfigurationInfo{}
	pacto.Status.Policies = []pactov1alpha1.PolicyInfo{}
	pacto.Status.Capabilities = []pactov1alpha1.CapabilityInfo{}

	// Contract info
	info := &pactov1alpha1.ContractInfo{
		ServiceName: c.Service.Name,
		Version:     c.Service.Version,
		ResolvedRef: lr.ResolvedRef,
	}
	if !c.Service.Owner.IsEmpty() {
		info.Owner = mapOwnerToInfo(c.Service.Owner)
		info.OwnerDisplay = c.Service.Owner.DisplayString()
	}
	pacto.Status.Contract = info
	pacto.Status.StatusFormat = pactov1alpha1.PactoStatusFormat

	// Interfaces (v2: Type is openapi/asyncapi/grpc, Ref not Contract, no Port)
	for _, iface := range c.Interfaces {
		ii := pactov1alpha1.InterfaceInfo{
			Name:       iface.Name,
			Type:       iface.Type,
			Ref:        iface.Ref,
			Visibility: iface.Visibility,
			// ponytail: plain conversions; a field added to the contract type
			// without one here stops the build instead of drifting silently.
			CompatibilityEpoch: int32(iface.CompatibilityEpoch),
		}
		if l := iface.Lifecycle; l != nil {
			ii.Lifecycle = new(pactov1alpha1.InterfaceLifecycleInfo(*l))
		}
		if g := iface.Guarantees; g != nil {
			ii.Guarantees = new(pactov1alpha1.GuaranteesInfo(*g))
		}
		pacto.Status.Interfaces = append(pacto.Status.Interfaces, ii)
	}

	// Capabilities (v2)
	for _, cap := range c.Capabilities {
		pacto.Status.Capabilities = append(pacto.Status.Capabilities, pactov1alpha1.CapabilityInfo{
			Type: cap.Type,
			Ref:  cap.Ref,
		})
	}

	// Configurations
	for _, cfg := range c.Configurations {
		ci := pactov1alpha1.ConfigurationInfo{
			Name:      cfg.Name,
			HasSchema: cfg.Schema != "",
			Ref:       cfg.Ref,
		}
		for k, v := range cfg.Values {
			ci.ValueKeys = append(ci.ValueKeys, k)
			if s, ok := v.(string); ok && strings.HasPrefix(s, "secret://") {
				ci.SecretKeys = append(ci.SecretKeys, k)
			}
		}
		sort.Strings(ci.ValueKeys)
		sort.Strings(ci.SecretKeys)
		// Surface configuration content (declared keys + types) so consumers can
		// render it without re-reading the bundle. Literal values win; otherwise
		// extract from the bundled schema.
		if len(cfg.Values) > 0 {
			ci.Properties = toSchemaProps(schemax.Values(cfg.Values))
		} else if cfg.Schema != "" {
			ci.Properties = schemaPropsFromBundle(lr.BundleFS, cfg.Schema)
		}
		pacto.Status.Configurations = append(pacto.Status.Configurations, ci)
	}

	// Dependencies
	for _, dep := range c.Dependencies {
		requires := make([]pactov1alpha1.RequirementInfo, 0, len(dep.Requires))
		for _, req := range dep.Requires {
			requires = append(requires, pactov1alpha1.RequirementInfo{Interface: req.Interface, GuaranteesInfo: pactov1alpha1.GuaranteesInfo(req.Guarantees)})
		}
		pacto.Status.Dependencies = append(pacto.Status.Dependencies, pactov1alpha1.DependencyInfo{
			Requires:      requires,
			Name:          dep.Name,
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
			External:      dep.External,
			Version:       dep.Version,
		})
	}

	// Policies
	for _, pol := range c.Policies {
		pi := pactov1alpha1.PolicyInfo{
			Name:      pol.Name,
			HasSchema: pol.Schema != "",
			Schema:    pol.Schema,
			Ref:       pol.Ref,
		}
		// Surface policy schema content (title/description + keys) for local
		// schemas so consumers can render it without re-reading the bundle.
		if pol.Schema != "" {
			pi.Properties = schemaPropsFromBundle(lr.BundleFS, pol.Schema)
			pi.Title, pi.Description = schemaMetaFromBundle(lr.BundleFS, pol.Schema)
		}
		pacto.Status.Policies = append(pacto.Status.Policies, pi)
	}

	// Metadata
	if len(c.Metadata) > 0 {
		pacto.Status.Metadata = make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			pacto.Status.Metadata[k] = fmt.Sprintf("%v", v)
		}
	}
}

// schemaPropsFromBundle reads a JSON Schema file from the bundle FS and returns
// its flattened properties as status entries. Returns nil when the bundle, file,
// or properties are unavailable (the dashboard then falls back to metadata only).
func schemaPropsFromBundle(fsys fs.FS, path string) []pactov1alpha1.SchemaProperty {
	if fsys == nil || path == "" {
		return nil
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	return toSchemaProps(schemax.Properties(data, path))
}

// schemaMetaFromBundle reads the title and description from a JSON Schema file
// in the bundle FS.
func schemaMetaFromBundle(fsys fs.FS, path string) (title, description string) {
	if fsys == nil || path == "" {
		return "", ""
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", ""
	}
	return schemax.Meta(data, path)
}

// toSchemaProps converts shared schemax properties into CRD status properties.
func toSchemaProps(ps []schemax.Property) []pactov1alpha1.SchemaProperty {
	if len(ps) == 0 {
		return nil
	}
	out := make([]pactov1alpha1.SchemaProperty, 0, len(ps))
	for _, p := range ps {
		out = append(out, pactov1alpha1.SchemaProperty{Key: p.Key, Value: p.Value, Type: p.Type})
	}
	return out
}

func (r *PactoReconciler) setCondition(pacto *pactov1alpha1.Pacto, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&pacto.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: pacto.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func mapValidationResult(vr validation.ValidationResult) *pactov1alpha1.ValidationResult {
	result := &pactov1alpha1.ValidationResult{Valid: len(vr.Errors) == 0}
	for _, e := range vr.Errors {
		result.Errors = append(result.Errors, pactov1alpha1.ValidationIssue{
			Code: e.Code, Path: e.Path, Message: e.Message,
		})
	}
	for _, w := range vr.Warnings {
		result.Warnings = append(result.Warnings, pactov1alpha1.ValidationIssue{
			Code: w.Code, Path: w.Path, Message: w.Message,
		})
	}
	return result
}

// summarizeFindings counts findings by severity. It does NOT rank them: the compliance
// ladder has one producer, validation.DeriveStatus (spec section 1.4).
func summarizeFindings(findings []finding.Finding) pactov1alpha1.Summary {
	var summary pactov1alpha1.Summary
	for _, f := range findings {
		switch f.Severity {
		case finding.SeverityError:
			summary.ErrorCount++
		case finding.SeverityUnknown:
			summary.UnknownCount++
		case finding.SeverityWarning:
			summary.WarningCount++
		case finding.SeverityInfo:
			summary.InfoCount++
		}
	}
	return summary
}

func formatValidationErrors(validationErrors []contract.ValidationError) string {
	var details []string
	for _, e := range validationErrors {
		if e.Path != "" {
			details = append(details, fmt.Sprintf("%s: %s", e.Path, e.Message))
		} else {
			details = append(details, e.Message)
		}
	}
	return fmt.Sprintf("Contract validation failed: %s", strings.Join(details, "; "))
}

// --- PactoRevision management ---

// setRuntimeObservedCondition records whether runtime evidence was collected. When
// any read failed (obsErr may join several, one per line) it reports False
// (ReasonObservationFailed) with every failure in the message, rather than
// claiming success: True means the collection fully succeeded.
func (r *PactoReconciler) setRuntimeObservedCondition(pacto *pactov1alpha1.Pacto, obsErr error) {
	if obsErr != nil {
		r.setCondition(pacto, pactov1alpha1.ConditionRuntimeObserved, metav1.ConditionFalse,
			pactov1alpha1.ReasonObservationFailed, "Runtime observation failed: "+strings.ReplaceAll(obsErr.Error(), "\n", "; "))
		return
	}
	r.setCondition(pacto, pactov1alpha1.ConditionRuntimeObserved, metav1.ConditionTrue,
		pactov1alpha1.ReasonFound, "Runtime evidence collected successfully")
}

// mapOwnerToInfo converts a contract.Owner to the CRD OwnerInfo representation.
// Owner is object-only (team, DRI, contacts); fields are mapped directly.
func mapOwnerToInfo(o contract.Owner) *pactov1alpha1.OwnerInfo {
	if o.IsEmpty() {
		return nil
	}
	result := &pactov1alpha1.OwnerInfo{
		Team: o.Team,
		DRI:  o.DRI,
	}
	for _, c := range o.Contacts {
		result.Contacts = append(result.Contacts, pactov1alpha1.OwnerContact{
			Type:    c.Type,
			Value:   c.Value,
			Purpose: c.Purpose,
		})
	}
	return result
}

// maxUnverified is the CRD's MaxItems on status.evaluationCoverage.unverified: a longer list would make
// the API server reject the whole status write.
const maxUnverified = 256

// capUnverified keeps the first maxUnverified of the engine's sorted paths, so the list stays sorted.
func capUnverified(paths []string) []string {
	if len(paths) > maxUnverified {
		return paths[:maxUnverified]
	}
	return paths
}
