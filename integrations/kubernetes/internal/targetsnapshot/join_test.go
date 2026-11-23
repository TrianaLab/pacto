/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package targetsnapshot

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"testing"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// The join block names the cluster, the installation key's fingerprint and
// the environments its Pactos' labels suggest, and a trust built from the
// block's own key, scoped to the cluster, verifies the snapshot.
func TestPublisherWritesJoinBlock(t *testing.T) {
	staging := pactoObj("pay", "pay", "sha256:aaaa", "Compliant")
	staging.SetLabels(map[string]string{core.EnvironmentLabel: "staging"})
	e := newTargetsEnv(t, fakeReader(staging))
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, bundle := e.pushed(t)
	j := pushedJoin(t, bundle)
	want := []core.AdvertisedEnvironment{{Name: "staging", Source: "label delivery.pacto.trianalab.io/environment on 1 Pacto"},
		{Name: "prod", Source: "the cluster name eu-prod"}}
	if j.Name != "eu-prod" || j.NameSource != fleet.ClusterNameFromNodeLabel || j.KeyID != signing.Hint(e.key.Public()) ||
		fmt.Sprint(j.Environments) != fmt.Sprint(want) {
		t.Fatalf("join %+v", j)
	}
}

// TestPublisherJoinRequestsFromFunc: the join block carries what Requests
// answers, and nothing about requests without it.
func TestPublisherJoinRequestsFromFunc(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	takes := &core.JoinRequests{Issuers: []string{"https://dex.example"}, Environments: []string{"production"}}
	for _, req := range []func() *core.JoinRequests{nil, func() *core.JoinRequests { return takes }} {
		e.p.Requests = req
		if err := e.p.publish(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, bundle := e.pushed(t)
		if got, want := pushedJoin(t, bundle).Requests, req != nil; (got != nil) != want || (want && fmt.Sprint(*got) != fmt.Sprint(*takes)) {
			t.Fatalf("Requests set %v: join requests %+v", want, got)
		}
	}
}

// pushedJoin is the join block of bundle, which must verify under a trust
// built from that block's public key, scoped to the cluster.
func pushedJoin(t *testing.T, bundle []byte) core.SnapshotJoin {
	t.Helper()
	st, _, err := core.OpenTargetSnapshot(bundle, nil, nil)
	if err != nil || st.Predicate.Join == nil {
		t.Fatalf("snapshot %+v, %v", st.Predicate, err)
	}
	j := *st.Predicate.Join
	der, _ := base64.StdEncoding.DecodeString(j.PublicKey)
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatal(err)
	}
	pem, _ := core.PublicKeyPEM(pub)
	trust := &core.TrustConfig{Keys: []core.TrustedKey{{Name: "member", PublicKey: pem, Streams: []string{snapCluster + "/*/*"}}}}
	if _, v, err := core.OpenTargetSnapshot(bundle, trust, signing.OpenKey); err != nil || !v.Verified || !v.Scoped {
		t.Fatalf("verdict %+v, %v", v, err)
	}
	return j
}

// A cluster the operator could not name publishes the uid's first 8
// characters as its name, and logs nothing about it.
func TestPublisherJoinNameFromUIDWhenUnnamed(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	p := forOperator(t, cfg)
	if p.ClusterName != snapCluster[:8] || p.ClusterNameSource != fleet.ClusterNameFromUID {
		t.Fatalf("name %q from %q", p.ClusterName, p.ClusterNameSource)
	}
	p.attempt(context.Background())
	if j := fetch(t, cfg).Join; j == nil || j.Name != "6f1e2c3d" || j.NameSource != fleet.ClusterNameFromUID || len(j.Environments) != 0 {
		t.Fatalf("join %+v", j)
	}
}

// A name that is not a DNS label (an EKS name with capitals or an
// underscore) is never published: the uid's name is.
func TestPublisherJoinNameNotADNSLabelUsesUID(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	for _, name := range []string{"EU_Prod", "eu-prod;rm"} {
		cfg.ClusterName, cfg.ClusterNameSource = name, fleet.ClusterNameFromNodeLabel
		if p := forOperator(t, cfg); p.ClusterName != snapCluster[:8] || p.ClusterNameSource != fleet.ClusterNameFromUID {
			t.Fatalf("%s: name %q from %q", name, p.ClusterName, p.ClusterNameSource)
		}
	}
	cfg.ClusterName = "eu-prod"
	if p := forOperator(t, cfg); p.ClusterName != "eu-prod" || p.ClusterNameSource != fleet.ClusterNameFromNodeLabel {
		t.Fatalf("a DNS label is kept: %q from %q", p.ClusterName, p.ClusterNameSource)
	}
}
