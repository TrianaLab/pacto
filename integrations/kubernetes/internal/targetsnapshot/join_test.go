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
	"errors"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

// stageObj is a Stage of service in environment, recording stream id when
// it is not empty.
func stageObj(namespace, name, service, env, id string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec":     map[string]any{"service": map[string]any{"name": service}, "environment": env},
	}}
	if id != "" {
		u.Object["status"] = map[string]any{"stream": map[string]any{"id": id}}
	}
	u.SetGroupVersionKind(gvkStageList.GroupVersion().WithKind("Stage"))
	return u
}

// publishedJoin publishes once and returns the snapshot and its join block.
func publishedJoin(t *testing.T, e *targetsEnv) (core.TargetSnapshot, core.SnapshotJoin) {
	t.Helper()
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, bundle := e.pushed(t)
	return s, pushedJoin(t, bundle)
}

// TestPublisherJoinNamesRecorderKey: the join block names the recorder key
// the operator was given.
func TestPublisherJoinNamesRecorderKey(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	pemKey, _ := core.PublicKeyPEM(e.key.Public())
	e.p.RecorderKey = &core.TrustedKey{Name: "recorder", PublicKey: pemKey}
	if _, j := publishedJoin(t, e); j.RecorderKey == nil || j.RecorderKey.PublicKey != pemKey {
		t.Fatalf("recorder key %+v", j.RecorderKey)
	}
}

// TestPublisherJoinWithoutRecorderTrust: with no recorder key the join block
// names none.
func TestPublisherJoinWithoutRecorderTrust(t *testing.T) {
	if _, j := publishedJoin(t, newTargetsEnv(t, fakeReader())); j.RecorderKey != nil {
		t.Fatalf("recorder key %+v", j.RecorderKey)
	}
}

// TestPublisherJoinListsRecordedStageStreams: the Stages that record a
// stream of this cluster are listed sorted; one with no stream id yet, one
// naming another cluster and one whose environment is not a DNS label are
// left out.
func TestPublisherJoinListsRecordedStageStreams(t *testing.T) {
	e := newTargetsEnv(t, fakeReader(
		stageObj("billing", "billing-prod", "billing", "production", snapCluster+"/billing/billing-prod"),
		stageObj("billing", "billing-dev", "billing", "dev", snapCluster+"/billing/billing-dev"),
		stageObj("billing", "fresh", "billing", "staging", ""),
		stageObj("billing", "copied", "billing", "staging", "other-uid/billing/copied"),
		stageObj("billing", "odd", "billing", "Prod", snapCluster+"/billing/odd")))
	e.p.StreamsRepository = "zot.test/acme/pacto-streams"
	s, j := publishedJoin(t, e)
	want := []core.JoinStream{
		{Service: core.ServiceRef{Name: "billing"}, Environment: "dev", Stream: snapCluster + "/billing/billing-dev"},
		{Service: core.ServiceRef{Name: "billing"}, Environment: "production", Stream: snapCluster + "/billing/billing-prod"}}
	if j.StreamsRepository != "zot.test/acme/pacto-streams" || j.StreamsCut || fmt.Sprint(j.Streams) != fmt.Sprint(want) ||
		s.Completeness != fleet.CompletenessComplete {
		t.Fatalf("streams %+v in %q, cut %v, completeness %s", j.Streams, j.StreamsRepository, j.StreamsCut, s.Completeness)
	}
}

// Over core.MaxJoinStreams Stages, the first in stream order are kept and the
// list is marked cut.
func TestPublisherJoinStreamsBounded(t *testing.T) {
	stages := make([]unstructured.Unstructured, core.MaxJoinStreams+1)
	for i := range stages {
		stages[i] = *stageObj("billing", fmt.Sprintf("s-%03d", i), "billing", "prod", fmt.Sprintf("%s/billing/s-%03d", snapCluster, i))
	}
	got, cut := joinStreams(snapCluster, stages)
	if len(got) != core.MaxJoinStreams || !cut || got[0].Stream != snapCluster+"/billing/s-000" {
		t.Fatalf("%d streams, cut %v", len(got), cut)
	}
}

// TestPublisherJoinStreamsCutOnListError: a Stage list that fails leaves the
// streams empty and cut, and the Pacto section partial with the note, never
// an empty "no Stages".
func TestPublisherJoinStreamsCutOnListError(t *testing.T) {
	r := interceptor.NewClient(fakeReader(), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if kindOf(list) == "Stage" {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		}})
	e := newTargetsEnv(t, r)
	e.p.StreamsRepository = "zot.test/acme/pacto-streams"
	s, j := publishedJoin(t, e)
	if !j.StreamsCut || j.Streams != nil || s.Completeness != fleet.CompletenessPartial ||
		len(s.Sources[0].Limitations) != 1 || s.Sources[0].Limitations[0].Source != "stages" {
		t.Fatalf("join %+v, sources %+v", j, s.Sources)
	}
}

// TestPublisherJoinNoStageListWithoutStreamsRepository: an operator without
// delivery lists no Stage and marks nothing partial.
func TestPublisherJoinNoStageListWithoutStreamsRepository(t *testing.T) {
	r := interceptor.NewClient(fakeReader(), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if kindOf(list) == "Stage" {
				t.Fatal("listed Stages without a streams repository")
			}
			return c.List(ctx, list, opts...)
		}})
	s, j := publishedJoin(t, newTargetsEnv(t, r))
	if j.StreamsRepository != "" || j.Streams != nil || j.StreamsCut || s.Completeness != fleet.CompletenessComplete {
		t.Fatalf("join %+v, completeness %s", j, s.Completeness)
	}
}

// ForOperator hands the recorder key and the streams repository to the
// publisher.
func TestForOperatorPassesRecorderKeyAndStreams(t *testing.T) {
	cfg := operatorConfig(t, newTestRegistry(t), fakeReader())
	cfg.RecorderKey, cfg.StreamsRepository = &core.TrustedKey{Name: "recorder"}, "zot.test/acme/pacto-streams"
	if p := forOperator(t, cfg); p.RecorderKey != cfg.RecorderKey || p.StreamsRepository != cfg.StreamsRepository {
		t.Fatalf("publisher %+v", p)
	}
}
