/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package targetsnapshot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// operatorConfig is a Config for the in-process registry reg.
func operatorConfig(t *testing.T, reg *testRegistry, c client.Reader) Config {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return Config{Registry: reg.host + "/acme", ClusterID: snapCluster, Namespace: "pacto-system",
		ServiceAccount: "pacto-operator", Key: key, Reader: c}
}

func forOperator(t *testing.T, cfg Config) *TargetPublisher {
	t.Helper()
	r, err := ForOperator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*TargetPublisher)
}

// fetch reads the snapshot cfg's publisher wrote and verifies it with cfg.Key.
func fetch(t *testing.T, cfg Config) core.TargetSnapshot {
	t.Helper()
	repo, _ := ocireferrer.Open(cfg.Registry+"/pacto-targets", ocireferrer.Options{})
	bundle, _, err := deliveryrecord.FetchTargetSnapshot(context.Background(), repo, "cluster-"+cfg.ClusterID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (signing.Policy{Key: cfg.Key.Public()}).Verify(bundle, nil); err != nil {
		t.Fatalf("signature: %v", err)
	}
	st, _, err := core.OpenTargetSnapshot(bundle, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return st.Predicate
}

func TestForOperatorNilWithoutRegistry(t *testing.T) {
	r, err := ForOperator(Config{ClusterID: snapCluster})
	if r != nil || err != nil {
		t.Fatalf("publisher %v, %v", r, err)
	}
}

func TestForOperatorEmptyClusterIDIsAnError(t *testing.T) {
	_, err := ForOperator(Config{Registry: "ghcr.io/acme"})
	if err == nil || !strings.Contains(err.Error(), "kube-system namespace UID") {
		t.Fatalf("err %v", err)
	}
}

func TestForOperatorBadSettingsAreErrors(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := ForOperator(Config{Registry: "Not A Host", ClusterID: snapCluster, Key: key}); err == nil ||
		!strings.Contains(err.Error(), "repository") {
		t.Fatalf("bad registry: %v", err)
	}
	if _, err := ForOperator(Config{Registry: "ghcr.io/acme", ClusterID: snapCluster}); err == nil ||
		!strings.Contains(err.Error(), "installation key") {
		t.Fatalf("no key: %v", err)
	}
}

// The publisher writes cluster-<uid> under <registry>/pacto-targets, signed
// with the installation key; from its second push it records the registry
// mode probed on its own tag.
func TestForOperatorPublishesToInstallationTargets(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader(snapshotObjects()...))
	p := forOperator(t, cfg)
	if p.Repo.Reference.Repository != "acme/pacto-targets" || p.Interval != core.TargetSnapshotInterval || p.Namespaces != nil {
		t.Fatalf("publisher %+v", p)
	}
	p.attempt(context.Background())
	if s := fetch(t, cfg); s.Cluster != snapCluster || s.RegistryMode != "" || len(s.Workloads) != 4 {
		t.Fatalf("first snapshot %+v", s)
	}
	p.attempt(context.Background())
	if s := fetch(t, cfg); s.RegistryMode == "" {
		t.Fatalf("second snapshot has no registry mode: %+v", s)
	}
}

func TestPublisherClaimsOperatorServiceAccount(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	forOperator(t, cfg).attempt(context.Background())
	if s := fetch(t, cfg); s.Publisher != "system:serviceaccount:pacto-system:pacto-operator" {
		t.Fatalf("publisher %q", s.Publisher)
	}
}

// A publisher whose repository sits outside the installation prefixes is
// refused by the push scope before any tag or manifest is written there.
func TestPublisherPushesThroughScope(t *testing.T) {
	reg := newTestRegistry(t)
	p := forOperator(t, operatorConfig(t, reg, fakeReader()))
	other, err := ocireferrer.Open(reg.host+"/other/pacto-targets", ocireferrer.Options{Scope: oci.NewPushScope(reg.host + "/acme")})
	if err != nil {
		t.Fatal(err)
	}
	p.Repo = other
	if err := p.publish(context.Background()); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("pushed outside the scope: %v", err)
	}
	plain, _ := ocireferrer.Open(reg.host+"/other/pacto-targets", ocireferrer.Options{})
	if _, _, err := deliveryrecord.FetchTargetSnapshot(context.Background(), plain, "cluster-"+snapCluster); err == nil {
		t.Fatal("a snapshot reached the repository outside the scope")
	}
}

func TestPublisherReportsEveryPush(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	var got []detect.PushResult
	cfg.OnPush = func(r detect.PushResult) { got = append(got, r) }
	p := forOperator(t, cfg)
	p.attempt(context.Background())
	reg.fault = func(r *http.Request) int {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return http.StatusUnauthorized
		}
		return 0
	}
	p.attempt(context.Background())
	ref := reg.host + "/acme/pacto-targets:cluster-" + snapCluster
	if len(got) != 2 || got[0].Error != "" || got[0].Ref != ref || got[0].At.IsZero() ||
		got[1].Ref != ref || !strings.Contains(got[1].Error, "401") {
		t.Fatalf("push results %+v", got)
	}
}

func TestPublisherReadsEvidenceFromManagedServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != evidenceingest.TargetsPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(evidenceBody))
	}))
	defer srv.Close()
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	cfg.EvidenceURL = srv.URL
	forOperator(t, cfg).attempt(context.Background())
	s := fetch(t, cfg)
	if len(s.Sources) != 3 || s.Sources[2].Kind != fleet.SourceKindEvidenceHTTP || s.Evidence == nil {
		t.Fatalf("snapshot %+v", s)
	}
}

// A scoped operator lists each watched namespace only and publishes a partial
// snapshot that names them, so no reader takes an unread namespace for empty.
func TestPublisherScopedNamespacesIsPartial(t *testing.T) {
	c := corev1.Container{Name: "app", Image: "reg.example/app:1"}
	objs := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "in-a"}, Spec: appsv1.DeploymentSpec{Template: podTemplate(c)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "b", Name: "in-b"}, Spec: appsv1.DeploymentSpec{Template: podTemplate(c)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "c", Name: "in-c"}, Spec: appsv1.DeploymentSpec{Template: podTemplate(c)}},
	}
	var clusterWide int
	r := interceptor.NewClient(fakeReader(objs...), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if (&client.ListOptions{}).ApplyOptions(opts).Namespace == "" {
				clusterWide++
			}
			return c.List(ctx, list, opts...)
		}})
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, r)
	cfg.Watched = []string{"a", "b"}
	forOperator(t, cfg).attempt(context.Background())
	s := fetch(t, cfg)
	if clusterWide != 0 || len(s.Workloads) != 2 || s.Completeness != fleet.CompletenessPartial ||
		len(s.Limitations) != 1 || s.Limitations[0].Code != fleet.LimitationTargetSnapshotScoped ||
		s.Limitations[0].Message != "read only namespaces a, b" {
		t.Fatalf("%d cluster-wide lists, snapshot %+v", clusterWide, s)
	}
	for _, sec := range s.Sources {
		if sec.Completeness != fleet.CompletenessPartial || sec.Limitations[0].Code != fleet.LimitationTargetSnapshotScoped {
			t.Errorf("section %+v", sec)
		}
	}
	// A failed namespace list leaves its section partial with both limitations.
	r = interceptor.NewClient(fakeReader(objs...), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errors.New("forbidden")
		}})
	cfg.Reader = r
	forOperator(t, cfg).attempt(context.Background())
	if s := fetch(t, cfg); len(s.Workloads) != 0 || len(s.Sources[1].Limitations) != 5 {
		t.Fatalf("snapshot %+v", s)
	}
}

// A probe that cannot tell records no mode.
func TestTargetsModeEmptyWhenProbeFails(t *testing.T) {
	reg := newTestRegistry(t)
	p := forOperator(t, operatorConfig(t, reg, fakeReader()))
	p.attempt(context.Background())
	reg.fault = func(r *http.Request) int {
		if strings.Contains(r.URL.Path, "/manifests/") {
			return 0
		}
		return http.StatusForbidden
	}
	if got := p.Mode(context.Background()); got != "" {
		t.Fatalf("mode %q", got)
	}
}

// The chart's owner policy travels inside the signed snapshot, so CLIs out of
// the cluster read the same default approvers the operator holds.
func TestTargetPublisherCarriesOwnerPolicy(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := operatorConfig(t, reg, fakeReader())
	cfg.OwnerPolicy = &core.OwnerPolicy{DefaultApprovers: []core.SignerRule{
		{Name: "platform-admins", Issuer: "https://accounts.google.com", Subject: `^admin@acme\.com$`}}}
	forOperator(t, cfg).attempt(context.Background())
	s := fetch(t, cfg)
	if s.OwnerPolicy == nil || len(s.OwnerPolicy.DefaultApprovers) != 1 ||
		s.OwnerPolicy.DefaultApprovers[0] != cfg.OwnerPolicy.DefaultApprovers[0] {
		t.Fatalf("owner policy %+v", s.OwnerPolicy)
	}
}

// Without a policy the snapshot carries none.
func TestTargetPublisherWithoutOwnerPolicyUnchanged(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.pushed(t); s.OwnerPolicy != nil {
		t.Fatalf("snapshot carries an owner policy: %+v", s.OwnerPolicy)
	}
}
