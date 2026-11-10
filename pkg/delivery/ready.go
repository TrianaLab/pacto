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
)

var readyFixes = map[string]string{
	ReadyReasonUnguarded: "install or repair the delivery admission policies and their bindings (delivery.enabled on the " +
		"pacto-operator chart) so the promoter admits operations",
	ReadyReasonCandidateNotFound: "create the candidate OCIRepository the Stage names in spec.candidates.ociRepositoryRef",
	ReadyReasonCandidateUnverified: "set spec.verify on the candidate OCIRepository with a secretRef or matchOIDCIdentity " +
		"naming the publisher, then publish with pacto publish",
	ReadyReasonUpstreamUntrusted:   "give the promoter the upstream recorder's key (promoter.upstreamTrust)",
	ReadyReasonUpstreamUnreadable:  "make the upstream records readable: check the repository, the registry credentials and the upstream trust key",
	ReadyReasonNoCandidateSource:   "set one of spec.candidates.ociRepositoryRef, upstreamStage or upstreamRecords",
	ReadyReasonApplierUnreadable:   "create the object spec.applyRef names, or install its CRD, and let the promoter read it",
	ReadyReasonApplierUnlabelled:   "label the applier delivery.pacto.trianalab.io/apply-ref=<stage>",
	ReadyReasonApplierMultiSource:  "give the Argo CD Application one OCI spec.source instead of spec.sources",
	ReadyReasonApplierNotOCI:       "point the Argo CD Application's spec.source.repoURL at an oci:// repository",
	ReadyReasonApplierNotAutomated: "set spec.syncPolicy.automated on the Argo CD Application",
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
