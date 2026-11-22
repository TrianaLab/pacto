package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
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
				Labels: map[string]string{"env": "production", "region": "us-west-2", "app.kubernetes.io/name": "campaign-service"}}}},
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

// demoClusterJoins is the hub's join list: what pacto cluster list prints and
// the Data sources page shows on the hub.
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
		} else {
			st.Reason = "no approval yet"
		}
		l.Clusters = append(l.Clusters, st)
	}
	return l
}
