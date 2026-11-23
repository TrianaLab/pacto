package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// linkedWebapp is webapp 1.0.0 linked by its image digest to
// shop/Deployment/webapp since asOf minus an hour, depending on orders at
// host/acme with compat.
func linkedWebapp(host, compat string, asOf time.Time) delivery.PactoView {
	since := asOf.Add(-time.Hour)
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "shop", "Deployment/webapp"
	p.Metadata.Labels = delivery.LinkLabels(fleet.ObservedLink{Signal: fleet.LinkSignalImageDigest, Value: "sha256:" + strings.Repeat("e", 64), Since: &since})
	p.Spec.Target = &delivery.PactoTargetView{WorkloadRef: &delivery.WorkloadRefView{Kind: "Deployment", Name: "webapp"}}
	p.Status.StatusFormat = delivery.StatusFormatGuarantees
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", asOf.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "webapp", Version: "1.0.0",
		ResolvedRef: "oci://" + host + "/acme/webapp@sha256:" + strings.Repeat("b", 64)}
	p.Status.Dependencies = []delivery.DependencyView{{Name: "orders", Ref: "oci://" + host + "/acme/orders", Required: true, Compatibility: compat}}
	return p
}

// linkedPayload is the linker's payload of cluster at asOf holding views.
func linkedPayload(t *testing.T, cluster string, asOf time.Time, views ...delivery.PactoView) []byte {
	t.Helper()
	b, err := delivery.EncodeLinked(delivery.LinkedTargets{SchemaVersion: delivery.LinkedSchemaVersion, Cluster: cluster,
		AsOf: asOf.Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Targets: views})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// operatorDir writes detected.json and, with linked, linked.json beside it,
// returning the detected file.
func operatorDir(t *testing.T, linked []byte) string {
	t.Helper()
	dir := t.TempDir()
	detected := filepath.Join(dir, detect.ConfigMapKey)
	if err := os.WriteFile(detected, detectedJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if linked != nil {
		if err := os.WriteFile(filepath.Join(dir, delivery.LinkedConfigMapKey), linked, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return detected
}

func sourceKinds(snap *fleet.FleetSnapshot) []string {
	var out []string
	for _, s := range snap.Sources {
		out = append(out, s.ID+"="+s.Kind)
	}
	return out
}

// TestDetectedRefAddsLinkedSource: --detected adds the linked source beside
// detection, over the same ConfigMap or from the file beside the detected
// one; a detected file alone adds none.
func TestDetectedRefAddsLinkedSource(t *testing.T) {
	now := func() time.Time { return detectedNow }
	svc := detectedService(t)
	snap, err := svc.Fleet(context.Background(), FleetOptions{Detected: operatorDir(t, linkedPayload(t, snapA, detectedNow, linkedWebapp("reg.example", "^1.0.0", detectedNow))), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceKinds(snap); !slices.Equal(got, []string{"detection=detection", "linked=" + fleet.SourceKindLinked}) || len(snap.Targets) != 1 {
		t.Fatalf("file: sources %q targets %d", got, len(snap.Targets))
	}
	snap, err = svc.Fleet(context.Background(), FleetOptions{Detected: operatorDir(t, nil), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceKinds(snap); !slices.Equal(got, []string{"detection=detection"}) {
		t.Fatalf("detected file alone: sources %q", got)
	}

	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-operator-detected", "namespace": "pacto-system", "labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data": map[string]any{detect.ConfigMapKey: string(detectedJSON(t)),
			delivery.LinkedConfigMapKey: string(linkedPayload(t, snapA, detectedNow, linkedWebapp("reg.example", "^1.0.0", detectedNow)))}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:detected.json":{}}}`)},
		{Manager: delivery.LinkerFieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:linked.json":{}}}`)},
	})
	svc.sys.newDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), nil
	}
	snap, err = svc.Fleet(context.Background(), FleetOptions{Detected: "k8s:pacto-system/pacto-operator-detected", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, snap, "linked"); st.Status != fleet.SourceAvailable || st.TargetCount != 1 || snap.Completeness != fleet.CompletenessComplete {
		t.Fatalf("k8s: linked %+v completeness %s", st, snap.Completeness)
	}

	svc.sys.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("loading kubeconfig: none") }
	snap, err = svc.Fleet(context.Background(), FleetOptions{Detected: "k8s:pacto-system/pacto-operator-detected", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, snap, "linked"); st.Status != fleet.SourceUnavailable || st.Error == nil || st.Error.Code != "NO_CLUSTER" {
		t.Fatalf("no client: linked %+v", st)
	}
}

// TestImpactCountsLinkedConsumer: a consumer the operator linked by its image
// digest is a running consumer like any target of its revision: webapp
// accepts orders ^1.0.0, so a BREAKING orders 2.0.0 blocks.
func TestImpactCountsLinkedConsumer(t *testing.T) {
	e := newSnapEnv(t)
	detected := operatorDir(t, linkedPayload(t, snapA, snapNow, linkedWebapp(e.host(), "^1.0.0", snapNow)))
	res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"),
		Fleet: FleetOptions{Detected: detected, Index: IndexNone, Now: func() time.Time { return snapNow }}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Gate != impact.GateBlocked || len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" || len(res.Consumers[0].Targets) != 1 {
		t.Fatalf("gate %s (%s), consumers %+v, limitations %+v", res.Gate, res.GateReason, res.Consumers, res.Limitations)
	}
}

// TestImpactSnapshotOnlyLinkedTargetGivesVerdict: a target snapshot whose
// only target is a linked one names its consumer and gives a verdict.
func TestImpactSnapshotOnlyLinkedTargetGivesVerdict(t *testing.T) {
	e := newSnapEnv(t)
	s := webappSnapshot(snapA, e.host(), "^1.0.0", snapNow)
	s.Pactos = []delivery.PactoView{linkedWebapp(e.host(), "^1.0.0", snapNow)}
	e.push(t, e.key, snapA, s)
	res := autoImpact(t, ordersService(t), e, e.ordersRef("2.0.0"), trustFile(t, e.scoped("rec", snapA)))
	if res.Gate != impact.GateBlocked || len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" {
		t.Fatalf("gate %s (%s), consumers %+v, limitations %+v", res.Gate, res.GateReason, res.Consumers, res.Limitations)
	}
}

// TestSnapshotLinkedTargetHandsOver: the snapshot's workloads section holds
// the twin of a linked target; the linked view hands it over to its service.
func TestSnapshotLinkedTargetHandsOver(t *testing.T) {
	e := newSnapEnv(t)
	s := ledgerSnapshot(snapA, snapNow)
	s.Pactos = append(s.Pactos, linkedWebapp(e.host(), "^1.0.0", snapNow))
	s.Workloads = append(s.Workloads, fleet.RawInferredService{Namespace: "shop", Kind: "Deployment", Name: "webapp"})
	e.push(t, e.key, snapA, s)
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	for k, svc := range snap.Services {
		if svc.Inferred() && strings.Contains(string(k), "webapp") {
			t.Errorf("inferred twin %s survived the hand-over", k)
		}
	}
	linked := 0
	for _, tr := range snap.Targets {
		if tr.Link != nil {
			linked++
		}
	}
	if linked != 1 {
		t.Fatalf("linked targets %d in %+v", linked, snap.Targets)
	}
}
