/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// manifestLayer is a gzipped tar of one manifests file.
func manifestLayer(t *testing.T, yaml string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "deploy.yaml", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(yaml))}))
	_, err := tw.Write([]byte(yaml))
	must(t, err)
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

const crbYAML = "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: orders-admin\n"

// TestOperationRejectedByManifestsNeverMovesPointer: with the inspector on,
// a version carrying a ClusterRoleBinding is rejected before the promoter
// writes its pointer, although the fake client's mapper calls every kind
// namespaced: the built-in kinds are judged first.
func TestOperationRejectedByManifestsNeverMovesPointer(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.op.Policy.ManifestInspector = true
	e.reg.layers = map[string][]byte{dB: manifestLayer(t, crbYAML)}
	o := e.drive("p1")
	if o.Status.Outcome != delivery.Rejected || !strings.Contains(o.Status.Reason+o.Status.Message, "pacto.manifests") ||
		!strings.Contains(o.Status.Reason+o.Status.Message, "ClusterRoleBinding orders-admin is cluster-scoped") || e.ociDigest("payments") != dA {
		t.Fatalf("want Rejected by pacto.manifests with the pointer unmoved, got %s %s %s (digest %s)", o.Status.Outcome, o.Status.Reason, o.Status.Message, e.ociDigest("payments"))
	}
	// A version of namespaced objects ships.
	e = baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.op.Policy.ManifestInspector = true
	e.reg.layers = map[string][]byte{dB: manifestLayer(t, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: payments\n")}
	if o := e.drive("p1"); !o.Status.Applied || e.ociDigest("payments") != dB {
		t.Fatalf("a clean version ships: %s %s", o.Status.Phase, o.Status.Message)
	}
	// Under the published refuseRawInfra (profile idp), a Crossplane managed
	// resource is rejected, even of a kind the cluster does not serve.
	e = baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.op.Policy.ManifestInspector, e.op.Policy.RefuseRawInfra = true, true
	e.reg.layers = map[string][]byte{dB: manifestLayer(t, "apiVersion: s3.aws.upbound.io/v1beta1\nkind: Bucket\nmetadata:\n  name: db\n")}
	if o := e.drive("p1"); o.Status.Outcome != delivery.Rejected || e.ociDigest("payments") != dA ||
		!strings.Contains(o.Status.Reason+o.Status.Message, "Bucket db is raw infrastructure") {
		t.Fatalf("want a claim Rejected under idp, got %s %s %s", o.Status.Outcome, o.Status.Reason, o.Status.Message)
	}
	// An applier the promoter cannot read leaves the artifact unread.
	s := stageObject(e.getStage())
	s.Spec.ApplyRef.Name = "gone"
	if in := (planSources{e.op.Cluster}).Manifests(context.Background(), s, dB); in.Verdict().Result != delivery.Unknown ||
		!strings.Contains(in.Unread[0], "the applier: ") {
		t.Fatalf("unreadable applier: %+v", in)
	}
}

// TestShadowPlanCountsManifestsRefusal: a version GitOps applied that the
// inspector refuses never counts as one the Stage would have promoted.
func TestShadowPlanCountsManifestsRefusal(t *testing.T) {
	e := observeEnv(t)
	e.st.Policy.ManifestInspector = true
	e.reg.layers = map[string][]byte{dC: manifestLayer(t, crbYAML)}
	e.setStage(func(st *delivery.StageStatus) { st.Published = []string{dA, dB, dC} })
	must(t, e.reconcileStage("payments-prod"))
	e.apply(dC)
	must(t, e.reconcileStage("payments-prod"))
	if c := shadowOf(e.getStage().Status, dC); c == nil || c.Predicted != delivery.PredictRejected || !strings.Contains(c.Question, "pacto.manifests") {
		t.Fatalf("the shadow plan counts the inspector's refusal: %+v", c)
	}
}

// mapped is a client whose RESTMapper the test sets.
type mapped struct {
	client.WithWatch
	m meta.RESTMapper
}

func (c mapped) RESTMapper() meta.RESTMapper { return c.m }

// TestDetectApplierNoMatchSetsNoAPI: a Stage whose applyRef kind the cluster
// does not serve reads Unknown with the no-applier message, for every kind.
func TestDetectApplierNoMatchSetsNoAPI(t *testing.T) {
	e := newEnv(t, stageObj(dA))
	e.st.Cluster.Client = mapped{e.c, meta.NewDefaultRESTMapper(nil)}
	for _, kind := range []string{"", delivery.ApplierHelmRelease, delivery.ApplierArgoCD} {
		s := stageObj(dA)
		s.Spec.ApplyRef.Kind = kind
		if st := e.detect(s); st.State != delivery.ApplierUnknown || st.Message != delivery.NoApplierMessage || st.Manager != "none" {
			t.Fatalf("%q: %+v", kind, st)
		}
	}
}

func TestScopeFrom(t *testing.T) {
	e := newEnv(t)
	scope := scopeFrom(e.c.RESTMapper())
	if ns, known := scope("rbac.authorization.k8s.io", "ClusterRoleBinding"); ns || !known {
		t.Fatal("the built-in kinds come first")
	}
	if ns, known := scope("source.toolkit.fluxcd.io", "OCIRepository"); !ns || !known {
		t.Fatal("a mapped kind")
	}
	if _, known := scope("example.com", "Widget"); known {
		t.Fatal("an unmapped kind is unknown")
	}
}
