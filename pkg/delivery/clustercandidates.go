package delivery

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// How a candidate stands against the hub's members.
const (
	CandidateMatched   = "matched"
	CandidateSameUID   = "same-uid"
	CandidateUnmatched = "unmatched"
)

// CandidatesID names the source of the limitations candidates add to a join
// list.
const CandidatesID = "cluster-candidates"

// ClusterCandidate is one cluster an inventory source of the hub knows and
// how it stands against the members: matched to an approved member under its
// approved key, stating the same UID as a member a person has not approved
// under that key, or not matched. A name never matches.
type ClusterCandidate struct {
	Source      string            `json:"source" enum:"argocd,flux,capi,ocm,rancher"`
	Kind        string            `json:"kind"`
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	// UID is the cluster UID the source object states, and UIDSource where.
	UID       string `json:"uid,omitempty"`
	UIDSource string `json:"uidSource,omitempty"`
	Match     string `json:"match" enum:"matched,same-uid,unmatched"`
	// Member is the approved member a matched candidate is; nil otherwise.
	Member *ClusterRef `json:"member,omitempty"`
	// Reason says why a candidate is not matched or only states the same UID.
	Reason string `json:"reason,omitempty"`
}

// InventoryEnvironment is one environment a matched candidate's label names:
// Source names the label and the object, Candidate the candidate.
type InventoryEnvironment struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Candidate string `json:"candidate"`
}

// inventoryEnvironmentLabels are the label keys that name an environment.
var inventoryEnvironmentLabels = []string{"env", "environment", fleet.TargetEnvironmentLabel}

// candidateObjects name the object each source's candidate is.
var candidateObjects = map[string]string{
	detect.SourceArgoCD:  "Argo CD cluster Secret",
	detect.SourceFlux:    "Flux kubeconfig",
	detect.SourceCAPI:    "Cluster API Cluster",
	detect.SourceOCM:     "OCM managed cluster",
	detect.SourceRancher: "Rancher cluster",
}

// sameUIDReason is why a candidate that states a member's UID is not matched.
const sameUIDReason = "the cluster reports this UID about itself; a person has not approved it under this key"

// MatchCandidates adds the candidates of c to l, read at now: each is matched
// only on an equal stated UID and only to a member approved under the key its
// snapshot names; a pending, removed or key-mismatched member with that UID
// gives same-uid. A matched candidate's environment labels add inventory
// environments to its member unless c is older than detect.StaleAfter.
// Environments, which the member signed, are never touched. A stale c or a
// source short of complete makes the candidates partial and says why; the
// members' completeness is never changed. A c with no asOf (none written)
// adds nothing.
func MatchCandidates(l ClusterJoinList, c detect.Clusters, now time.Time) ClusterJoinList {
	if c.AsOf.IsZero() {
		return l
	}
	stale := now.Sub(c.AsOf) > detect.StaleAfter
	membersPartial := l.Completeness == fleet.CompletenessPartial
	l.Clusters = slices.Clone(l.Clusters)
	l.CandidatesAsOf = c.AsOf.UTC().Format(time.RFC3339)
	l.CandidatesCompleteness = fleet.CompletenessComplete
	l.Candidates = make([]ClusterCandidate, 0, len(c.Candidates))
	for _, d := range c.Candidates {
		cand, member := matchCandidate(l, d, membersPartial)
		if member >= 0 && !stale {
			addInventoryEnvironments(&l.Clusters[member], d)
		}
		l.Candidates = append(l.Candidates, cand)
	}
	return candidateLimitations(l, c, stale)
}

// matchCandidate is d against l's members, and the index of the member it
// matched (-1 for none).
func matchCandidate(l ClusterJoinList, d detect.Candidate, membersPartial bool) (ClusterCandidate, int) {
	cand := ClusterCandidate{Source: d.Source, Kind: d.Kind, Namespace: d.Namespace, Name: d.Name, DisplayName: d.DisplayName,
		Labels: maps.Clone(d.Labels), UID: d.UID, UIDSource: d.UIDSource, Match: CandidateUnmatched}
	i := slices.IndexFunc(l.Clusters, func(m ClusterJoinState) bool { return d.UID != "" && m.UID == d.UID })
	switch {
	case d.UID == "":
		cand.Reason = "the candidate states no cluster UID"
	case d.UID == l.Hub.UID:
		cand.Reason = "the candidate states the hub's own UID"
	case i >= 0 && approvedUnderKey(l.Clusters[i]):
		m := l.Clusters[i]
		cand.Match, cand.Member = CandidateMatched, &ClusterRef{UID: m.UID, Name: m.Name}
		return cand, i
	case i >= 0:
		cand.Match, cand.Reason = CandidateSameUID, sameUIDReason
	case membersPartial:
		cand.Reason = "the member list is partial, so UID " + shortClusterUID(d.UID) + " may belong to a member not read"
	default:
		cand.Reason = "no member publishes UID " + shortClusterUID(d.UID)
	}
	return cand, -1
}

// approvedUnderKey reports whether m is approved under the key its snapshot
// names: only then is its self-reported UID an identity.
func approvedUnderKey(m ClusterJoinState) bool {
	return m.State == JoinApproved && m.KeyID != "" && m.KeyID == m.ApprovedKeyID
}

// shortClusterUID is a cluster UID as a reason names it.
func shortClusterUID(uid string) string { return uid[:min(8, len(uid))] }

// addInventoryEnvironments adds to m the environments d's labels name, by
// name once, up to maxAdvertisedEnvironments.
func addInventoryEnvironments(m *ClusterJoinState, d detect.Candidate) {
	ref := candidateRef(d)
	for _, key := range inventoryEnvironmentLabels {
		v := d.Labels[key]
		if !dnsLabel(v) || len(m.InventoryEnvironments) >= maxAdvertisedEnvironments ||
			slices.ContainsFunc(m.InventoryEnvironments, func(e InventoryEnvironment) bool { return e.Name == v }) {
			continue
		}
		m.InventoryEnvironments = append(m.InventoryEnvironments, InventoryEnvironment{Name: v,
			Source: "label " + key + "=" + v + " on " + candidateObject(d) + " " + ref, Candidate: d.Source + ":" + ref})
	}
}

// candidateRef is d's object as namespace/name, or name.
func candidateRef(d detect.Candidate) string {
	if d.Namespace == "" {
		return d.Name
	}
	return d.Namespace + "/" + d.Name
}

// candidateObject names what d is: a Flux candidate by the kind it names.
func candidateObject(d detect.Candidate) string {
	if d.Source == detect.SourceFlux {
		return candidateObjects[d.Source] + " " + d.Kind
	}
	return candidateObjects[d.Source]
}

// candidateLimitations makes l's candidates partial with why when c is stale
// or one of its sources is short of complete.
func candidateLimitations(l ClusterJoinList, c detect.Clusters, stale bool) ClusterJoinList {
	var lims []fleet.Limitation
	if stale {
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationSourceStale, Source: CandidatesID,
			Message: "the candidate clusters were read at " + l.CandidatesAsOf + ", over " + detect.StaleAfter.String() + " ago, so no inventory environment applies"})
	}
	for _, s := range c.Sources {
		code := fleet.LimitationSourcePartial
		switch s.Completeness {
		case detect.Complete:
			continue
		case detect.Unknown:
			code = fleet.LimitationSourceUnavailable
		}
		lims = append(lims, fleet.Limitation{Code: code, Source: CandidatesID,
			Message: strings.TrimSuffix(fmt.Sprintf("the candidate clusters from %s are %s: %s", s.Source, s.Completeness, s.Reason), ": ")})
	}
	if len(lims) > 0 {
		l.Limitations = append(slices.Clone(l.Limitations), lims...)
		l.CandidatesCompleteness = fleet.CompletenessPartial
	}
	return l
}
