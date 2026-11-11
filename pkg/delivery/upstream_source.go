package delivery

import (
	"cmp"
	"slices"
)

// Upstream is where a Stage's candidates come from, as its spec declares.
type Upstream struct {
	Kind        string `json:"kind" enum:"stage,records,artifact,none" doc:"stage: another Stage in this cluster; records: a Stage in another cluster, read from its signed records; artifact: new artifacts of an OCIRepository; none: the Stage declares no source, so it never promotes"`
	Namespace   string `json:"namespace,omitempty" doc:"kind stage: the upstream Stage's namespace"`
	Stage       string `json:"stage,omitempty" doc:"kind stage: the upstream Stage's name"`
	Environment string `json:"environment,omitempty" doc:"kind stage: the upstream Stage's environment, when it could be read"`
	Problem     string `json:"problem,omitempty" enum:"missing,unreadable,outOfScope,self,unverified" doc:"kind stage: why the upstream Stage could not be read; outOfScope when it sits outside the namespace the dashboard shows or the request names; self when the Stage names itself, so it waits on itself and never promotes. kind artifact, from the Stage's Ready condition: missing when the OCIRepository does not exist, unverified when it verifies no publisher's signature; either way the Stage never promotes"`
	Stream      string `json:"stream,omitempty" doc:"kind records: the upstream Stage's stream id, <clusterID>/<namespace>/<name>"`
	Repository  string `json:"repository,omitempty" doc:"kind records: the upstream recorder's streams repository; kind artifact: the OCIRepository's name"`
	Trust       string `json:"trust,omitempty" enum:"trusted,untrusted,unreadable,unknown" doc:"kind records: whether the promoter trusts that record stream, from the Stage's Ready condition. trusted: it reads and verifies the records; untrusted: it trusts no key for the records of other clusters, so the Stage never promotes; unreadable: the records cannot be read or verified; unknown: the promoter has not said yet"`
}

// recordsTrust is whether the promoter trusts st's upstream record stream, as
// its Ready condition says: the promoter sets UpstreamUntrusted when it holds
// no upstream key and UpstreamUnreadable when the records fail to verify.
func recordsTrust(st *StageObject) string {
	i := slices.IndexFunc(st.Status.Conditions, func(c Condition) bool { return c.Type == ConditionReady })
	switch {
	case i < 0:
		return "unknown"
	case readyReason(st) == ReadyReasonUpstreamUntrusted:
		return "untrusted"
	case readyReason(st) == ReadyReasonUpstreamUnreadable:
		return "unreadable"
	case st.Status.Conditions[i].Status == "True":
		return "trusted"
	}
	// Not Ready for another reason (its applier): the records were not judged.
	return "unknown"
}

// readyReason is the reason of st's Ready condition, empty when it has none.
func readyReason(st *StageObject) string {
	if i := slices.IndexFunc(st.Status.Conditions, func(c Condition) bool { return c.Type == ConditionReady }); i >= 0 {
		return st.Status.Conditions[i].Reason
	}
	return ""
}

// UpstreamListed is where st's candidates come from, from the Stages already
// listed (byStage), so a list of many rows reads no Stage twice. An upstream
// Stage not in the list is missing only when read says the listing read its
// namespace in full. Outside scope (a namespace the reader is limited to;
// empty for none) it is out of scope; else it is unreadable: it may sit in a
// namespace the caller cannot list. The other kinds need no read.
func UpstreamListed(st *StageObject, byStage map[string]*StageObject, read func(ns string) bool, scope string) *Upstream {
	c := st.Spec.Candidates
	switch {
	case c.UpstreamStage != nil:
		return stageListed(st, byStage, read, scope)
	case c.UpstreamRecords != nil:
		return &Upstream{Kind: "records", Stream: c.UpstreamRecords.Stream, Repository: c.UpstreamRecords.Repository, Trust: recordsTrust(st)}
	case c.OCIRepositoryRef != nil:
		u := &Upstream{Kind: "artifact", Repository: c.OCIRepositoryRef.Name}
		switch readyReason(st) {
		case ReadyReasonCandidateNotFound:
			u.Problem = "missing"
		case ReadyReasonCandidateUnverified:
			u.Problem = "unverified"
		}
		return u
	}
	return &Upstream{Kind: "none"}
}

// stageListed is UpstreamListed for an upstream Stage.
func stageListed(st *StageObject, byStage map[string]*StageObject, read func(ns string) bool, scope string) *Upstream {
	c := st.Spec.Candidates
	u := &Upstream{Kind: "stage", Namespace: cmp.Or(c.UpstreamStage.Namespace, st.Metadata.Namespace), Stage: c.UpstreamStage.Name}
	switch up := byStage[u.Namespace+"/"+u.Stage]; {
	case u.Namespace == st.Metadata.Namespace && u.Stage == st.Metadata.Name:
		u.Problem = "self"
	case up != nil:
		u.Environment = up.Spec.Environment
	case read(u.Namespace):
		u.Problem = "missing"
	case scope != "" && u.Namespace != scope:
		u.Problem = "outOfScope"
	default:
		u.Problem = "unreadable"
	}
	return u
}
