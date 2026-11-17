package dashboard

import "github.com/trianalab/pacto/v4/pkg/fleet"

// notMissing are the limitations a namespace-scoped source can carry that
// mean no record is missing: its scope, the SOURCE_PARTIAL Build derives from
// it, a runtime-observed name or revision link it could not resolve, and a
// cross-source merge conflict (Build keeps those out of a source's record
// problems, see ingestCollection). Any other code naming the source refuses,
// so an unknown code is never read as complete.
var notMissing = map[string]bool{
	fleet.LimitationK8sNamespaceScoped:         true,
	fleet.LimitationSourcePartial:              true,
	fleet.LimitationObservedIdentityUnresolved: true,
	fleet.LimitationRevisionAmbiguous:          true,
	fleet.LimitationRevisionDocConflict:        true,
	fleet.LimitationRevisionLockConflict:       true,
	fleet.LimitationTargetRefConflict:          true,
	fleet.LimitationTargetFieldConflict:        true,
}

// BackstageServable reports whether snap, the unrestricted snapshot, may replace the
// entities Backstage holds, and otherwise why not. Backstage reads a catalog
// as whole and orphans every entity missing from it, so it is refused while
// the organization catalog is unread, a source is unavailable, a source is
// partial for any reason but reading the namespace list it was given, or the
// fleet is empty. A stale source keeps its records and is served. It is the
// one rule `pacto export backstage` and the dashboard route share. Judge it
// before any reader's restriction: a restricted view is always partial, and
// its narrowing drops limitations by prose, so it cannot say what is missing.
func BackstageServable(snap *fleet.FleetSnapshot) (reason string, ok bool) {
	if snap.Completeness == fleet.CompletenessComplete {
		return "", true
	}
	scoped := map[string]bool{}
	for _, l := range snap.Limitations {
		switch l.Code {
		case fleet.LimitationExternalCatalogUnavailable:
			return "source " + l.Source + " is " + string(fleet.SourceUnavailable), false
		case fleet.LimitationK8sNamespaceScoped:
			scoped[l.Source] = true
		}
	}
	missing := map[string]bool{}
	for _, l := range snap.Limitations {
		if !notMissing[l.Code] {
			missing[l.Source] = true
		}
	}
	for _, st := range snap.Sources {
		if st.Status == fleet.SourceUnavailable || st.Status == fleet.SourcePartial && (!scoped[st.ID] || missing[st.ID]) {
			return "source " + st.ID + " is " + string(st.Status), false
		}
	}
	if len(snap.Services) == 0 && len(snap.Targets) == 0 {
		return "the fleet is empty", false
	}
	return "", true
}
