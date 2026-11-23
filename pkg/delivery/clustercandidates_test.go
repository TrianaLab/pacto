package delivery

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	candHub   = "00000000-0000-4000-8000-000000000000"
	candEU    = "9f1c4e2a-0000-4000-8000-000000000001"
	candUS    = "9f1c4e2b-0000-4000-8000-000000000002"
	candOther = "9f1c4e2c-0000-4000-8000-000000000003"
)

var candNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

// approved is a member approved under the key its snapshot names.
func approved(uid, name string) ClusterJoinState {
	return ClusterJoinState{UID: uid, Name: name, State: JoinApproved, KeyID: "k1", ApprovedKeyID: "k1",
		Environments: []AdvertisedEnvironment{{Name: "production", Source: "the cluster name " + name}}}
}

// joinList is a complete list of the hub with members ms.
func joinList(ms ...ClusterJoinState) ClusterJoinList {
	return ClusterJoinList{Hub: ClusterRef{UID: candHub}, Completeness: fleet.CompletenessComplete, Clusters: ms}
}

// argo is an Argo CD candidate named name stating uid with labels.
func argo(name, uid string, labels map[string]string) detect.Candidate {
	c := detect.Candidate{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: name, DisplayName: name, Labels: labels, UID: uid}
	if uid != "" {
		c.UIDSource = "annotation " + detect.ClusterUIDAnnotation
	}
	return c
}

// clusters is a fresh complete inventory read of cs.
func clusters(cs ...detect.Candidate) detect.Clusters {
	return detect.Clusters{SchemaVersion: detect.SchemaVersion, AsOf: candNow.Add(-time.Minute), Total: len(cs),
		Sources: []detect.InventorySource{{Source: detect.SourceArgoCD, Completeness: detect.Complete}}, Candidates: cs}
}

func TestMatchByStatedUID(t *testing.T) {
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(argo("eu-prod-secret", candEU, nil)), candNow)
	c := l.Candidates[0]
	if c.Match != CandidateMatched || !reflect.DeepEqual(c.Member, &ClusterRef{UID: candEU, Name: "eu-prod"}) || c.Reason != "" || c.UIDSource == "" {
		t.Errorf("candidate = %+v", c)
	}
	if l.CandidatesAsOf != "2026-11-23T09:59:00Z" || l.CandidatesCompleteness != fleet.CompletenessComplete || len(l.Limitations) != 0 {
		t.Errorf("list = %+v", l)
	}
}

func TestMatchNeverByName(t *testing.T) {
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(argo("eu-prod", candUS, nil), argo("eu-prod", "", nil)), candNow)
	for _, c := range l.Candidates {
		if c.Match != CandidateUnmatched || c.Member != nil {
			t.Errorf("name matched: %+v", c)
		}
	}
}

func TestTwoCandidatesOneUIDBothMatch(t *testing.T) {
	capi := detect.Candidate{Source: detect.SourceCAPI, Kind: "Cluster", Namespace: "fleet", Name: "eu", UID: candEU}
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(argo("eu", candEU, nil), capi), candNow)
	if l.Candidates[0].Match != CandidateMatched || l.Candidates[1].Match != CandidateMatched {
		t.Errorf("candidates = %+v", l.Candidates)
	}
}

func TestUnmatchedReasons(t *testing.T) {
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(argo("a", "", nil), argo("b", candOther, nil), argo("c", candHub, nil)), candNow)
	want := []string{"the candidate states no cluster UID", "no member publishes UID 9f1c4e2c", "the candidate states the hub's own UID"}
	for i, c := range l.Candidates {
		if c.Match != CandidateUnmatched || c.Reason != want[i] {
			t.Errorf("%d = %+v, want %q", i, c, want[i])
		}
	}
}

func TestUnmatchedReasonOnPartialList(t *testing.T) {
	members := joinList(approved(candEU, "eu-prod"))
	members.Completeness = fleet.CompletenessPartial
	l := MatchCandidates(members, clusters(argo("b", candOther, nil)), candNow)
	if r := l.Candidates[0].Reason; r != "the member list is partial, so UID 9f1c4e2c may belong to a member not read" {
		t.Errorf("reason = %q", r)
	}
}

func TestPendingMemberGetsNoInventoryEnvironment(t *testing.T) {
	for _, st := range []JoinState{JoinPending, JoinRemoved} {
		m := approved(candEU, "eu-prod")
		m.State = st
		l := MatchCandidates(joinList(m), clusters(argo("eu", candEU, map[string]string{"env": "prod"})), candNow)
		if c := l.Candidates[0]; c.Match != CandidateSameUID || c.Member != nil || c.Reason != sameUIDReason || l.Clusters[0].InventoryEnvironments != nil {
			t.Errorf("%s: %+v %+v", st, c, l.Clusters[0])
		}
	}
}

func TestKeyMismatchGetsNoInventoryEnvironment(t *testing.T) {
	for _, key := range []string{"k2", ""} {
		m := approved(candEU, "eu-prod")
		m.KeyID = key
		l := MatchCandidates(joinList(m), clusters(argo("eu", candEU, map[string]string{"env": "prod"})), candNow)
		if c := l.Candidates[0]; c.Match != CandidateSameUID || l.Clusters[0].InventoryEnvironments != nil {
			t.Errorf("key %q: %+v %+v", key, c, l.Clusters[0])
		}
	}
}

func TestInventoryEnvironmentAppended(t *testing.T) {
	m := approved(candEU, "eu-prod")
	before := fmt.Sprintf("%+v", m.Environments)
	flux := detect.Candidate{Source: detect.SourceFlux, Kind: "Secret", Namespace: "apps", Name: "eu-kubeconfig", UID: candEU,
		Labels: map[string]string{"environment": "prod", fleet.TargetEnvironmentLabel: "staging"}}
	l := MatchCandidates(joinList(m), clusters(argo("eu-prod", candEU, map[string]string{"env": "prod", "region": "eu"}), flux), candNow)
	got := l.Clusters[0].InventoryEnvironments
	want := []InventoryEnvironment{
		{Name: "prod", Source: "label env=prod on Argo CD cluster Secret argocd/eu-prod", Candidate: "argocd:argocd/eu-prod"},
		{Name: "staging", Source: "label " + fleet.TargetEnvironmentLabel + "=staging on Flux kubeconfig Secret apps/eu-kubeconfig", Candidate: "flux:apps/eu-kubeconfig"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inventory environments = %+v", got)
	}
	if after := fmt.Sprintf("%+v", l.Clusters[0].Environments); after != before {
		t.Errorf("environments changed: %s -> %s", before, after)
	}
	// The 16 bound, and a candidate with no namespace names its object by name.
	var cands []detect.Candidate
	for i := range 20 {
		cands = append(cands, detect.Candidate{Source: detect.SourceRancher, Kind: "Cluster", Name: fmt.Sprint("c", i), UID: candEU, Labels: map[string]string{"env": fmt.Sprint("e", i)}})
	}
	l = MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(cands...), candNow)
	if envs := l.Clusters[0].InventoryEnvironments; len(envs) != maxAdvertisedEnvironments || envs[0].Source != "label env=e0 on Rancher cluster c0" {
		t.Errorf("bounded = %d %+v", len(envs), envs[0])
	}
}

func TestInventoryEnvironmentOnlyFromDNSLabels(t *testing.T) {
	labels := map[string]string{"env": "Prod", "environment": "a b", fleet.TargetEnvironmentLabel: strings.Repeat("x", 64), "tier": "prod"}
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), clusters(argo("eu", candEU, labels)), candNow)
	if envs := l.Clusters[0].InventoryEnvironments; envs != nil {
		t.Errorf("inventory environments = %+v", envs)
	}
}

func TestUnreadCandidatesArePartial(t *testing.T) {
	c := clusters(argo("eu", "", nil))
	c.Sources = []detect.InventorySource{{Source: detect.SourceArgoCD, Completeness: detect.Unknown, Reason: "the operator may not list secrets"},
		{Source: detect.SourceFlux, Completeness: detect.Partial, Reason: "labels not read in apps"}, {Source: detect.SourceCAPI, Completeness: detect.Partial}}
	l := MatchCandidates(joinList(), c, candNow)
	want := []fleet.Limitation{
		{Code: fleet.LimitationSourceUnavailable, Source: CandidatesID, Message: "the candidate clusters from argocd are unknown: the operator may not list secrets"},
		{Code: fleet.LimitationSourcePartial, Source: CandidatesID, Message: "the candidate clusters from flux are partial: labels not read in apps"},
		{Code: fleet.LimitationSourcePartial, Source: CandidatesID, Message: "the candidate clusters from capi are partial"},
	}
	if l.CandidatesCompleteness != fleet.CompletenessPartial || l.Completeness != fleet.CompletenessComplete || !reflect.DeepEqual(l.Limitations, want) {
		t.Errorf("list = %s %s %+v", l.CandidatesCompleteness, l.Completeness, l.Limitations)
	}
}

func TestStaleClustersArePartial(t *testing.T) {
	c := clusters(argo("eu", candEU, map[string]string{"env": "prod"}))
	c.AsOf = candNow.Add(-detect.StaleAfter - time.Second)
	l := MatchCandidates(joinList(approved(candEU, "eu-prod")), c, candNow)
	if l.CandidatesCompleteness != fleet.CompletenessPartial || l.Completeness != fleet.CompletenessComplete || len(l.Limitations) != 1 ||
		l.Limitations[0].Code != fleet.LimitationSourceStale || !strings.Contains(l.Limitations[0].Message, "no inventory environment applies") {
		t.Errorf("list = %s %+v", l.CandidatesCompleteness, l.Limitations)
	}
	if l.Candidates[0].Match != CandidateMatched || l.Clusters[0].InventoryEnvironments != nil {
		t.Errorf("stale = %+v %+v", l.Candidates[0], l.Clusters[0])
	}
}

func TestNoClustersAddsNothing(t *testing.T) {
	in := joinList(approved(candEU, "eu-prod"))
	if l := MatchCandidates(in, detect.Clusters{}, candNow); !reflect.DeepEqual(l, in) {
		t.Errorf("list = %+v", l)
	}
	if l := MatchCandidates(in, clusters(), candNow); l.Candidates == nil || len(l.Candidates) != 0 || l.CandidatesAsOf == "" {
		t.Errorf("an empty read is an empty list: %+v", l)
	}
}
