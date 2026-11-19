package delivery

import "slices"

// The Stage conditions the promoter writes.
const (
	// ConditionReady says whether the Stage can promote, and why not.
	ConditionReady = "Ready"
	// ConditionWatched says whether the running version is still re-checked
	// after release.
	ConditionWatched = "Watched"
)

// The reasons of a Ready condition that is False: what keeps the Stage from
// promoting. ReadyFix says the change that clears each one.
const (
	ReadyReasonUnguarded           = "Unguarded"
	ReadyReasonCandidateNotFound   = "CandidateNotFound"
	ReadyReasonCandidateUnverified = "CandidateUnverified"
	ReadyReasonUpstreamUntrusted   = "UpstreamUntrusted"
	ReadyReasonUpstreamUnreadable  = "UpstreamUnreadable"
	ReadyReasonNoCandidateSource   = "NoCandidateSource"
	ReadyReasonApplierUnreadable   = "ApplierUnreadable"
	ReadyReasonApplierUnlabelled   = "ApplierUnlabelled"
	ReadyReasonApplierMultiSource  = "ApplierMultiSource"
	ReadyReasonApplierNotOCI       = "ApplierNotOCI"
	ReadyReasonApplierNotAutomated = "ApplierNotAutomated"
	ReadyReasonApplierConflict     = "ApplierConflict"
	ReadyReasonApplierUnknown      = "ApplierUnknown"
	ReadyReasonApplierNotChartRef  = "ApplierNotChartRef"
	// ReadyReasonRegistryUnreadable: the repository candidates.registry
	// watches cannot be listed.
	ReadyReasonRegistryUnreadable = "RegistryUnreadable"
	// ReadyReasonPublisherUnverifiable: the promoter has no publisher check
	// or no Sigstore trusted root to judge candidates.registry with.
	ReadyReasonPublisherUnverifiable = "PublisherUnverifiable"
)

var readyFixes = map[string]string{
	ReadyReasonUnguarded: "install or repair the delivery admission policies and their bindings (delivery.enabled on the " +
		"pacto-operator chart) so the promoter admits operations",
	ReadyReasonCandidateNotFound: "create the candidate OCIRepository the Stage names in spec.candidates.ociRepositoryRef",
	ReadyReasonCandidateUnverified: "set spec.verify on the candidate OCIRepository with a secretRef or matchOIDCIdentity " +
		"naming the publisher, then publish with pacto publish",
	ReadyReasonUpstreamUntrusted:  "give the promoter the upstream recorder's key (promoter.upstreamTrust)",
	ReadyReasonUpstreamUnreadable: "make the upstream records readable: check the repository, the registry credentials and the upstream trust key",
	ReadyReasonNoCandidateSource:  "set one of spec.candidates.registry, ociRepositoryRef, upstreamStage or upstreamRecords",
	ReadyReasonRegistryUnreadable: "make the watched repository listable: check spec.candidates.registry.repository (or the applier's " +
		"repository) and the promoter's registry credentials",
	ReadyReasonPublisherUnverifiable: "give the promoter a publisher check (delivery.publishers.registry) and a Sigstore trusted root " +
		"it can read (delivery.publishers.trustedRoot, or public-good Sigstore reachable over TUF)",
	ReadyReasonApplierUnreadable:   "create the object spec.applyRef names, or install its CRD, and let the promoter read it",
	ReadyReasonApplierUnlabelled:   "label the applier delivery.pacto.trianalab.io/apply-ref=<stage>",
	ReadyReasonApplierMultiSource:  "give the Argo CD Application one OCI spec.source instead of spec.sources",
	ReadyReasonApplierNotOCI:       "point the Argo CD Application's spec.source.repoURL at an oci:// repository",
	ReadyReasonApplierNotAutomated: "set spec.syncPolicy.automated on the Argo CD Application",
	ReadyReasonApplierConflict: "point spec.applyRef at the manager status.applier names, or move the workloads to the Stage's applier; " +
		"Pacto never takes a workload from another manager on its own",
	ReadyReasonApplierUnknown: "deliver the workloads through Argo CD or Flux so they carry its labels, or let the promoter read them; " +
		"status.applier says what Pacto saw",
	ReadyReasonApplierNotChartRef: "point the HelmRelease's spec.chartRef at an OCIRepository; a HelmChart pins a version, never a digest",
}

// ReadyFix is the change that clears a Ready reason, or "" for a reason it
// does not know.
func ReadyFix(reason string) string {
	return readyFixes[reason]
}

// readyOf is the Stage's Ready condition, or nil when it has none.
func readyOf(st StageStatus) *Condition {
	if i := slices.IndexFunc(st.Conditions, func(c Condition) bool { return c.Type == ConditionReady }); i >= 0 {
		return &st.Conditions[i]
	}
	return nil
}
