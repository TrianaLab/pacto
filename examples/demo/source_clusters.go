package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The demo's production cluster is a hub: two clusters that publish to its
// registry were approved as members and one waits for a person. A member's
// targets reach the fleet the way the hub's join reader reads them, from its
// signed snapshot verified under its approved key; the pending cluster is in
// the join list only, never a source. Member targets run in namespaces the
// hub never uses, so every published 3-part href keeps naming one target.

// demoTargetsRepository is the hub's advertised snapshot repository.
const demoTargetsRepository = "demo.pacto.local/pacto-targets"

// demoMember is one cluster that publishes to the hub's registry.
type demoMember struct {
	uid, name, nameSource string
	state                 delivery.JoinState
	snapshotAge           time.Duration
	digest                string // the snapshot manifest the hub read
	targets               []fleet.RawTarget
	allow                 []string // its delivery.remote.allow.environments
}

// demoApprover is the person who approved both members.
const demoApprover = "oidc:alice@northwind.example"

func demoMembers() []demoMember {
	at := func(d time.Duration) *time.Time { t := demoNow().Add(-d); return &t }
	cov := &fleet.Coverage{Evaluated: 5, Required: 5}
	return []demoMember{
		{uid: "9f1c4e2a-3b7d-4e19-a2c5-6d8f0b1e7a34", name: "eu-prod", nameSource: fleet.ClusterNameFromNodeLabel, state: delivery.JoinApproved,
			snapshotAge: 2 * time.Minute, digest: "4be1a9c07d3f5e2b8a61c9d4f0e7b3a25c8d1f6e9b0a4c7d2e5f8a1b3c6d9e0f",
			targets: []fleet.RawTarget{{Scope: "eu-payments", Kind: "Deployment", Name: "fraud-service", Service: "fraud-service",
				ResolvedRef: demoRegistry + "fraud-service:1.0.1", Compliance: fleet.StatusCompliant, Coverage: cov,
				EvidenceAt: at(2 * time.Minute), ReconciledAt: at(2 * time.Minute),
				Labels: map[string]string{"env": "production", "region": "eu-central-1", "app.kubernetes.io/name": "fraud-service"}}}},
		{uid: "c2d8a6f0-5e1b-4a73-9c4d-8b2f6e0a1d95", name: "us-west", nameSource: fleet.ClusterNameFromContext, state: delivery.JoinApproved,
			snapshotAge: 9 * time.Minute, digest: "7d2c5e8f1a4b9c0d3e6f2a5b8c1d4e7f0a3b6c9d2e5f8a1b4c7d0e3f6a9b2c5d",
			targets: []fleet.RawTarget{{Scope: "us-ledger", Kind: "Deployment", Name: "campaign-service", Service: "campaign-service",
				ResolvedRef: demoRegistry + "campaign-service:1.1.0", Compliance: fleet.StatusUnknown, Coverage: &fleet.Coverage{Evaluated: 3, Required: 5},
				EvidenceAt: at(9 * time.Minute), ReconciledAt: at(9 * time.Minute),
				Labels: map[string]string{"env": "production", "region": "us-west-2", "app.kubernetes.io/name": "campaign-service"}}},
			allow: []string{"test", "prod"}},
		{uid: "e7b3d915-4a2c-4f8e-b6d1-0c9a5f3e2b17", name: "ap-dev", nameSource: fleet.ClusterNameFromContext, state: delivery.JoinPending,
			snapshotAge: 5 * time.Minute, digest: "a1c4e7f0b3d6a9c2e5f8b1d4a7c0e3f6b9d2a5c8e1f4b7d0a3c6e9f2b5d8a1c4"},
	}
}

// demoMemberKey is the public key m's installation holds and its key id, the
// sha256 of its PKIX DER, both as a join block carries them.
func demoMemberKey(m demoMember) (string, string) {
	seed := sha256.Sum256([]byte("demo member key " + m.uid))
	der, err := x509.MarshalPKIXPublicKey(ed25519.NewKeyFromSeed(seed[:]).Public())
	if err != nil {
		panic(err) // an Ed25519 key always marshals
	}
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(der), hex.EncodeToString(sum[:])
}

// demoMemberSources are the approved members' sources, read the way the join
// reader reads them: one target-snapshot source per member, verified under
// its approved key. Both are fresh: a stale or partial target source makes
// every service's placement unknown, which would empty the Golden paths tab
// (the reason edge-cluster is an observation source, source_fleet.go).
func demoMemberSources() []fleet.Source {
	var out []fleet.Source
	for _, m := range demoMembers() {
		if m.state != delivery.JoinApproved {
			continue
		}
		cl := &fleet.Cluster{ID: m.uid, Name: m.name, NameSource: m.nameSource}
		id, synced := "targets-"+m.uid, demoNow().Add(-m.snapshotAge)
		st := &fleet.SourceState{Status: fleet.SourceAvailable, Ref: demoTargetsRepository + "@sha256:" + m.digest,
			Signer: "member:" + m.name, Cluster: cl, LastSuccessfulSync: &synced}
		out = append(out, fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, &fleet.Collection{Cluster: cl, Targets: m.targets, State: st}))
	}
	return out
}

// demoIssuer is the issuer the hub signs people in with; its length makes the
// opened Data sources HelpTip wrap.
const demoIssuer = "https://login.northwind.example/realms/production-engineering/protocol/openid-connect"

// demoMemberLinks are the hub's member link facts, carried by an advisory
// source that is listed nowhere: eu-prod's channel is live and its apiserver
// accepts the hub's exchanged tokens; us-west's dropped with its snapshot and
// reads under the group mapping alice approved, which raises
// CLUSTER_ISSUER_DIFFERS. The pending ap-dev never connected.
func demoMemberLinks() fleet.Source {
	at := func(d time.Duration) *time.Time { t := demoNow().Add(-d); return &t }
	links := map[string]fleet.ClusterLink{}
	for _, m := range demoMembers() {
		switch m.name {
		case "eu-prod":
			links[m.uid] = fleet.ClusterLink{Channel: fleet.LinkLive, Since: at(3 * time.Hour),
				Issuers: []fleet.IssuerModel{{Issuer: demoIssuer, Model: fleet.IssuerShared}}}
		case "us-west":
			links[m.uid] = fleet.ClusterLink{Channel: fleet.LinkDown, Since: at(m.snapshotAge),
				Issuers: []fleet.IssuerModel{{Issuer: demoIssuer, Model: fleet.IssuerGroupMapping, MappedBy: demoApprover,
					MappedAt: at(48 * time.Hour), Mapping: "platform-engineers=pacto-readers"}}}
		}
	}
	return fleet.NewMemorySource("member-links", fleet.SourceKindMemberLinks, &fleet.Collection{Advisory: true, ClusterLinks: links})
}

// demoClusters is what the hub's operator found in its Argo CD and Cluster
// API: eu-prod's cluster Secret states eu-prod's UID and env=prod, the
// staging-west Secret states none, and ap-dev-pool is a Cluster API cluster.
//
//go:embed clusters.json
var demoClusters []byte

// demoChartVersion is the chart version the demo hub hands out.
const demoChartVersion = "6.6.0"

// demoCandidates is demoClusters read now, as the hub reads clusters.json.
func demoCandidates() detect.Clusters {
	c, err := detect.DecodeClusters(demoClusters)
	if err != nil {
		panic(err) // the embedded file is fixed
	}
	c.AsOf = demoNow()
	return c
}

// demoClusterInstall is the hub's member install for the engine from, over
// the demo candidates, as the dashboard download hands it out.
func demoClusterInstall(_ context.Context, from string) ([]byte, error) {
	l, c := demoClusterJoins(), demoCandidates()
	res, err := delivery.MemberInstall(delivery.MemberInstallOptions{Engine: from,
		Registry: strings.TrimSuffix(demoTargetsRepository, "/"+delivery.TargetSnapshotRepository), ChartVersion: demoChartVersion,
		Argo: c.From(detect.SourceArgoCD), Flux: c.From(detect.SourceFlux), Matched: l.Candidates, HubUID: l.Hub.UID})
	if err != nil {
		return nil, err
	}
	return delivery.MemberInstallDownload(res), nil
}

// demoClusterJoins is the hub's join list: what pacto cluster list prints and
// the Data sources page shows on the hub, with the candidate clusters matched
// by the UID they state.
func demoClusterJoins() delivery.ClusterJoinList {
	l := delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: prodClusterID, Name: "production"}, Repository: demoTargetsRepository,
		AsOf: demoNow().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, CanApprove: true}
	for _, m := range demoMembers() {
		pub, keyID := demoMemberKey(m)
		st := delivery.ClusterJoinState{UID: m.uid, Name: m.name, NameSource: m.nameSource, KeyID: keyID, PublicKey: pub, State: m.state,
			Environments: delivery.AdvertisedEnvironments(nil, m.name, m.nameSource),
			SnapshotAsOf: demoNow().Add(-m.snapshotAge).Format(time.RFC3339), Status: fleet.SourceAvailable}
		if m.state == delivery.JoinApproved {
			rec := sha256.Sum256([]byte("join " + m.uid))
			st.Approver, st.ApprovedAt = demoApprover, demoNow().Add(-72*time.Hour).Format(time.RFC3339)
			st.Record = demoTargetsRepository + "@sha256:" + hex.EncodeToString(rec[:])
			st.ApprovedKeyID, st.ApprovedPublicKey = keyID, pub
			// Its intake takes requests for what its name hosts (eu-prod: prod),
			// kept to its allow list, or that list when the name hosts none.
			hosted := delivery.RequestAllowlist{Environments: m.allow}.Hosted(delivery.AdvertisedEnvironments(nil, m.name, m.nameSource))
			st.Requests = &delivery.JoinRequests{Issuers: []string{demoIssuer}, Environments: hosted}
		} else {
			st.Reason = "no approval yet"
		}
		l.Clusters = append(l.Clusters, st)
	}
	return delivery.MatchCandidates(l, demoCandidates(), demoNow())
}

// demoMemberRequest is one request alice signed on the hub for a member, and
// the member's furthest signed answer to it. Fixed data: nothing here signed
// or verified either record.
type demoMemberRequest struct {
	member  string
	request delivery.VerifiedRequest
	outcome delivery.RequestOutcome
}

// demoMemberRequests: alice asked both production members to promote
// payments in prod. eu-prod applied it; us-west's promoter rejected the
// digest she named, a copy pushed with plain oras that no trusted publisher
// signed, at its pacto.published gate.
func demoMemberRequests() []demoMemberRequest {
	sum := sha256.Sum256([]byte("oras copy payments@2.4.2"))
	unsigned := "sha256:" + hex.EncodeToString(sum[:])
	at := demoNow().Add(-40 * time.Minute)
	ask := func(member, uid, digest string) demoMemberRequest {
		rec := sha256.Sum256([]byte("request " + uid + " " + digest))
		manifest := "sha256:" + hex.EncodeToString(rec[:])
		op := &delivery.RequestedOperation{Namespace: story.Prod, Name: "payments-req-" + manifest[7:17], Stage: "payments"}
		return demoMemberRequest{member: member,
			request: delivery.VerifiedRequest{Manifest: manifest, SignedAt: at, Request: delivery.OperationRequest{Cluster: uid, Hub: prodClusterID,
				Service: delivery.ServiceRef{Name: "payments"}, Environment: story.Prod, Version: "2.4.2", Digest: digest, Requester: story.Alice.User,
				Placement: delivery.RequestPlacement{Source: delivery.RequestPlacedAdvertised, ConfirmedBy: story.Alice.User}}},
			outcome: delivery.RequestOutcome{Request: manifest, Cluster: uid, State: delivery.RequestConcluded, Operation: op, Digest: digest,
				Outcome: delivery.Succeeded, At: at.Add(6 * time.Minute).Format(time.RFC3339)}}
	}
	eu, us := ask("eu-prod", "9f1c4e2a-3b7d-4e19-a2c5-6d8f0b1e7a34", story.ArtifactDigest("payments", "2.4.2")),
		ask("us-west", "c2d8a6f0-5e1b-4a73-9c4d-8b2f6e0a1d95", unsigned)
	// One propose signed both, each naming the placement it confirmed.
	both := []string{eu.request.Request.Cluster, us.request.Request.Cluster}
	eu.request.Request.Placement.Members, us.request.Request.Placement.Members = both, both
	us.outcome.Outcome = delivery.Rejected
	us.outcome.Published = "Fail: no publisher check accepted " + unsigned[:19] + " on " + strings.TrimPrefix(demoRegistry, "oci://") + "payments: see pacto delivery trust payments"
	return []demoMemberRequest{eu, us}
}

// demoMemberPlans are the hub's member plans for service in envs and the
// environments its members take requests for: placed by Pacto's own
// MemberPlacement over the join list (the demo has no placement rule), each
// placed member in the state its newest request's answer reads as on a hub.
// payments is the only service a person asked members for.
func demoMemberPlans(service string, envs []string) []delivery.MemberPlan {
	list, svc := demoClusterJoins(), delivery.ServiceRef{Name: service}
	var verified []delivery.VerifiedRequest
	asked := map[string]demoMemberRequest{}
	for _, r := range demoMemberRequests() {
		if r.request.Request.Service == svc {
			verified = append(verified, r.request)
			asked[r.request.Request.Cluster+"/"+r.request.Request.Environment] = r
		}
	}
	asOf := map[string]string{}
	for _, c := range list.Clusters {
		asOf[c.UID] = c.SnapshotAsOf
	}
	var out []delivery.MemberPlan
	for _, env := range delivery.MemberEnvironments(envs, list.Clusters) {
		p := delivery.MemberPlan{Service: svc, Environment: env, Placement: delivery.MemberPlacement(nil, list.Clusters, svc, env, verified),
			Members: []delivery.MemberState{}, Completeness: fleet.CompletenessComplete, AsOf: list.AsOf,
			OrderUnknown: !slices.ContainsFunc(envs, func(e string) bool { return delivery.SameEnvironment(e, env) }),
			Next:         delivery.ConfirmCommand(delivery.Request{Service: service, Environment: env}, demoTargetsRepository)}
		if len(p.Placement.Placed) == 0 {
			continue
		}
		for _, m := range p.Placement.Placed {
			st := delivery.MemberState{UID: m.UID, Name: m.Name, State: delivery.MemberNoRequest, AsOf: asOf[m.UID], Stale: m.Stale}
			if r, ok := asked[m.UID+"/"+env]; ok {
				st = demoMemberState(st, r.outcome)
			}
			p.Members = append(p.Members, st)
		}
		out = append(out, p)
	}
	return out
}

// demoMemberState is st as a hub reads a concluded outcome o: applied on
// success, refused with the pacto.published verdict when the gates
// rejected it.
func demoMemberState(st delivery.MemberState, o delivery.RequestOutcome) delivery.MemberState {
	st.State, st.Digest, st.Reason = delivery.MemberApplied, o.Digest, string(o.Outcome)
	if o.Outcome == delivery.Rejected {
		st.State, st.Reason = delivery.MemberRefused, string(o.Outcome)+": "+o.Published
	}
	st.Operation = o.Operation.Namespace + "/" + o.Operation.Name
	return st
}

// demoMemberRefusal is us-west's rejected promote as a refused-list row, the
// way a hub lists a member's refusal: check member, the member's name.
func demoMemberRefusal() dashboard.PublishRefusal {
	r := demoMemberRequests()[1]
	o := r.outcome
	return dashboard.PublishRefusal{Service: r.request.Request.Service.Name, Version: r.request.Request.Version, Check: "member", Cluster: r.member,
		Namespace: o.Operation.Namespace, Stage: o.Operation.Stage, Digest: o.Digest, Code: string(o.Outcome), Reason: o.Published,
		Actor: r.request.Request.Requester, At: o.At}
}
