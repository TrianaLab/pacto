package delivery

import (
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// PlacementUnconfirmed is the source of a member placement inferred from the
// environments the members take requests for, before a person confirmed it
// by signing (then it reads RequestPlacedAdvertised).
const PlacementUnconfirmed = "unconfirmed"

// PlacedMember is one member a promote is placed on: why (Evidence),
// whether its snapshot is stale (its request waits until it reads the
// registry) and whether a live channel reviews for it.
type PlacedMember struct {
	UID        string `json:"uid"`
	Name       string `json:"name"`
	Evidence   string `json:"evidence"`
	Stale      bool   `json:"stale,omitempty"`
	Reviewable bool   `json:"reviewable,omitempty"`
}

// UnplacedMember is an approved member a promote is not placed on, and why.
type UnplacedMember struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MemberPlacementPlan is where a promote goes among a hub's members: the
// members, the members left out with why, where that came from (a signed
// placement rule, by digest; the environments the members take requests
// for, unconfirmed; or advertised once a person confirmed it by signing a
// request) and who confirmed it.
type MemberPlacementPlan struct {
	Placed      []PlacedMember   `json:"clusters"`
	Unplaced    []UnplacedMember `json:"unplaced,omitempty"`
	Source      string           `json:"source" enum:"policy,advertised,unconfirmed"`
	Policy      string           `json:"policy,omitempty"`
	ConfirmedBy string           `json:"confirmedBy,omitempty"`
}

// MemberPlacement places service in env on the approved members of states:
// on the members the first rule of policy that places it names by uid
// (policy), else on those whose join block takes requests for env
// (unconfirmed). A member that takes no requests or names no environment is
// left out with why; a pending or removed one is never named. Once a request
// in requests (verified, for the same service, environment and digest)
// exists, an unconfirmed placement reads advertised, confirmed by the newest
// one's requester. The caller sets Policy.
func MemberPlacement(policy *PlacementPolicy, states []ClusterJoinState, service ServiceRef, env string, requests []VerifiedRequest) MemberPlacementPlan {
	plan := MemberPlacementPlan{Placed: []PlacedMember{}, Source: PlacementUnconfirmed}
	var rule *PlacementRule
	if policy != nil {
		if i := slices.IndexFunc(policy.Rules, func(r PlacementRule) bool { return len(r.ClusterUIDs) > 0 && r.Places(service.Name, env) }); i >= 0 {
			rule, plan.Source = &policy.Rules[i], PlacementFromPolicy
		}
	}
	for _, s := range states {
		if s.State != JoinApproved || (rule != nil && !slices.Contains(rule.ClusterUIDs, s.UID)) {
			continue
		}
		evidence, why := memberHosts(s, env, rule)
		switch {
		case why != "":
			plan.Unplaced = append(plan.Unplaced, UnplacedMember{UID: s.UID, Name: s.Name, Reason: why})
		case evidence != "":
			plan.Placed = append(plan.Placed, PlacedMember{UID: s.UID, Name: s.Name, Evidence: evidence, Stale: s.Status == fleet.SourceStale})
		}
	}
	if newest := newestRequest(requests, service, env); plan.Source == PlacementUnconfirmed && newest != nil {
		plan.Source, plan.ConfirmedBy = RequestPlacedAdvertised, newest.Request.Requester
	}
	return plan
}

// newestRequest is the newest of requests for service in env, nil for none.
func newestRequest(requests []VerifiedRequest, service ServiceRef, env string) *VerifiedRequest {
	var newest *VerifiedRequest
	for i, r := range requests {
		if r.Request.Service == service && SameEnvironment(r.Request.Environment, env) &&
			(newest == nil || r.SignedAt.After(newest.SignedAt)) {
			newest = &requests[i]
		}
	}
	return newest
}

// memberHosts says why member s hosts env (a placement rule names it, or it
// takes requests for env), why it is left out, or neither when it simply
// does not host env.
func memberHosts(s ClusterJoinState, env string, rule *PlacementRule) (evidence, why string) {
	switch {
	case s.Requests == nil:
		return "", "does not take requests: set delivery.remote.enabled, a registry and delivery there"
	case rule != nil:
		return "placement rule " + rule.Name, ""
	case len(s.Requests.Environments) == 0:
		return "", "advertises no environment: name it in a placement rule"
	case slices.ContainsFunc(s.Requests.Environments, func(e string) bool { return SameEnvironment(e, env) }):
		return "takes requests for " + strings.Join(s.Requests.Environments, ", "), ""
	}
	return "", ""
}

// MemberEnvironments are the environments a service's member placements
// are read for: envs (the hub's own, in their order), then each environment
// an approved member of states takes requests for that envs does not name
// already, by canonical name, sorted.
func MemberEnvironments(envs []string, states []ClusterJoinState) []string {
	out := slices.Clone(envs)
	seen := map[string]bool{}
	for _, e := range envs {
		seen[canonicalEnvironment(e)] = true
	}
	var more []string
	for _, s := range states {
		if s.State != JoinApproved || s.Requests == nil {
			continue
		}
		for _, e := range s.Requests.Environments {
			if c := canonicalEnvironment(e); !seen[c] {
				seen[c], more = true, append(more, e)
			}
		}
	}
	slices.Sort(more)
	return append(out, more...)
}
