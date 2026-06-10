package dashboard

// computeSectionMeta fills d.SectionMeta so the UI can render a stable, fully
// explained set of sections: each section reports whether it is present, empty,
// not-applicable, or unavailable, plus which source supplied it.
//
// contractSource is the bundle source that won the contract base ("local",
// "oci", "cache") or "" when the only data came from k8s. runtimeEvaluated is
// true when a k8s runtime overlay was applied (the operator observed the
// service); it gates the runtime-only sections.
func computeSectionMeta(d *ServiceDetails, contractSource string, runtimeEvaluated bool) {
	isReference := d.ContractStatus == StatusReference

	// Where the definition (interfaces/configs/policies/deps/readiness/runtime)
	// came from. With no bundle, that data — when present — comes from the
	// operator's CR status mirror.
	defSource := contractSource
	hasBundle := contractSource != "" && contractSource != "k8s"
	if defSource == "" {
		defSource = "k8s"
	}

	meta := make(map[string]SectionInfo, 14)

	// Definition sections: present if declared, else genuinely empty.
	meta[SectionInterfaces] = defSection(len(d.Interfaces) > 0, defSource)
	meta[SectionConfigurations] = defSection(len(d.Configurations) > 0, defSource)
	meta[SectionPolicies] = defSection(len(d.Policies) > 0, defSource)
	meta[SectionDependencies] = defSection(len(d.Dependencies) > 0, defSource)
	meta[SectionReadiness] = defSection(d.Readiness != nil && len(d.Readiness.Checks) > 0, defSource)
	meta[SectionRuntime] = defSection(d.Runtime != nil, defSource)

	// Validation is computed for every contract; "empty" means valid (no issues).
	hasIssues := d.Validation != nil && (len(d.Validation.Errors) > 0 || len(d.Validation.Warnings) > 0)
	if hasIssues {
		meta[SectionValidation] = SectionInfo{State: SectionPresent, Source: defSource}
	} else {
		meta[SectionValidation] = SectionInfo{State: SectionEmpty, Source: defSource, Reason: "no validation issues"}
	}

	// Docs live only in a bundle; the operator never mirrors them.
	switch {
	case len(d.Docs) > 0:
		meta[SectionDocs] = SectionInfo{State: SectionPresent, Source: defSource}
	case hasBundle:
		meta[SectionDocs] = SectionInfo{State: SectionEmpty, Source: defSource, Reason: "no docs/*.md packed in this contract bundle"}
	default:
		meta[SectionDocs] = SectionInfo{State: SectionUnavailable, Reason: "documentation requires a contract bundle (not available from the cluster)"}
	}

	// Runtime-only sections come from the operator and apply only to deployed
	// workloads that were actually observed.
	rt := func(present bool) SectionInfo { return runtimeSection(present, isReference, runtimeEvaluated) }
	meta[SectionObservedRuntime] = rt(d.ObservedRuntime != nil)
	meta[SectionRuntimeDiff] = rt(len(d.RuntimeDiff) > 0)
	meta[SectionResources] = rt(d.Resources != nil)
	meta[SectionPorts] = rt(d.Ports != nil)
	meta[SectionEndpoints] = rt(len(d.Endpoints) > 0)
	meta[SectionConditions] = rt(len(d.Conditions) > 0)

	d.SectionMeta = meta
}

// defSection returns the state for a definition section.
func defSection(present bool, source string) SectionInfo {
	if present {
		return SectionInfo{State: SectionPresent, Source: source}
	}
	return SectionInfo{State: SectionEmpty, Source: source, Reason: "none declared in this contract"}
}

// runtimeSection returns the state for a runtime-only (k8s) section.
func runtimeSection(present, isReference, runtimeEvaluated bool) SectionInfo {
	switch {
	case isReference:
		return SectionInfo{State: SectionNotApplicable, Reason: "reference contract — no runtime target"}
	case !runtimeEvaluated:
		return SectionInfo{State: SectionUnavailable, Reason: "runtime not observed — no cluster connection"}
	case present:
		return SectionInfo{State: SectionPresent, Source: "k8s"}
	default:
		return SectionInfo{State: SectionEmpty, Source: "k8s"}
	}
}
