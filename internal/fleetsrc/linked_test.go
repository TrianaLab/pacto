package fleetsrc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const linkedCluster = "0f3c9a1e-7d2b-4c11-9e55-2a8f6b1c0d33"

var linkedDigest = "sha256:" + strings.Repeat("a", 64)

// linkedView is payments 2.3.0 linked to shop/Deployment/payments by its
// image digest an hour before detectionNow.
func linkedView() delivery.PactoView {
	since := detectionNow.Add(-time.Hour)
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "shop", "Deployment/payments"
	p.Metadata.Labels = delivery.LinkLabels(fleet.ObservedLink{Signal: fleet.LinkSignalImageDigest, Value: linkedDigest, Since: &since})
	p.Spec.Target = &delivery.PactoTargetView{WorkloadRef: &delivery.WorkloadRefView{Kind: "Deployment", Name: "payments"}}
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", detectionNow.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "payments", Version: "2.3.0",
		ResolvedRef: "oci://ghcr.io/acme/contracts/payments@sha256:" + strings.Repeat("c", 64)}
	return p
}

// linkedJSON is linked targets dated asOf with completeness c and lims.
func linkedJSON(t *testing.T, asOf time.Time, c fleet.Completeness, lims ...fleet.Limitation) []byte {
	t.Helper()
	b, err := delivery.EncodeLinked(delivery.LinkedTargets{SchemaVersion: delivery.LinkedSchemaVersion, Cluster: linkedCluster,
		AsOf: asOf.Format(time.RFC3339), Completeness: c, Limitations: lims, Targets: []delivery.PactoView{linkedView()}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeLinked(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), delivery.LinkedConfigMapKey)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func collectLinked(t *testing.T, s *LinkedSource) *fleet.Collection {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || col == nil || col.State == nil || col.Advisory {
		t.Fatalf("Collect = %+v, %v; want a collection with a state", col, err)
	}
	return col
}

func assertLinkedUnavailable(t *testing.T, col *fleet.Collection, code string) {
	t.Helper()
	if col.State.Status != fleet.SourceUnavailable || col.State.Error == nil || col.State.Error.Code != code || len(col.Targets) != 0 {
		t.Errorf("state = %+v error %+v targets %d, want unavailable %s", col.State, col.State.Error, len(col.Targets), code)
	}
}

// operatorConfigMap is the -detected ConfigMap holding both keys, the
// detection key owned by detectionManager and the linked key by
// linkedManager.
func operatorConfigMap(t *testing.T, linked []byte, detectionManager, linkedManager string) *unstructured.Unstructured {
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-operator-detected", "namespace": "pacto-system",
			"labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data": map[string]any{detect.ConfigMapKey: string(detectionJSON(t, detectionNow, detect.Complete)), delivery.LinkedConfigMapKey: string(linked)}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: detectionManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:detected.json":{}}}`)},
		{Manager: linkedManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:linked.json":{}}}`)},
	})
	return cm
}

func k8sLinked(objs ...runtime.Object) *LinkedSource {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Version: "v1", Resource: "configmaps"}: "ConfigMapList"}, objs...)
	return NewLinkedK8sSource("linked", dyn, "pacto-system", "pacto-operator-detected", clockAt(detectionNow))
}

// TestLinkedSourceReadsTargetsWithLink: a linked target reads as a
// Kubernetes target of its revision with its link, in its cluster.
func TestLinkedSourceReadsTargetsWithLink(t *testing.T) {
	asOf := detectionNow.Add(-time.Minute)
	s := NewLinkedFileSource("linked", writeLinked(t, linkedJSON(t, asOf, fleet.CompletenessComplete)), clockAt(detectionNow))
	if s.ID() != "linked" || s.Kind() != fleet.SourceKindLinked {
		t.Errorf("id %q kind %q", s.ID(), s.Kind())
	}
	col := collectLinked(t, s)
	if col.State.Status != fleet.SourceAvailable || !col.State.ObservedAt.Equal(asOf) || col.State.Error != nil || len(col.Limitations) != 0 {
		t.Fatalf("state %+v limitations %+v", col.State, col.Limitations)
	}
	if col.Cluster == nil || col.Cluster.ID != linkedCluster || col.Cluster.NameSource != fleet.ClusterNameFromUID {
		t.Errorf("cluster = %+v", col.Cluster)
	}
	if len(col.Targets) != 1 || col.Targets[0].Link == nil || col.Targets[0].Link.Value != linkedDigest || col.Targets[0].Service != "payments" {
		t.Fatalf("targets = %+v", col.Targets)
	}
}

// TestLinkedSourceReadsConfigMap: through the ConfigMap too, when the linker
// owns the key.
func TestLinkedSourceReadsConfigMap(t *testing.T) {
	col := collectLinked(t, k8sLinked(operatorConfigMap(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete), detect.FieldManager, delivery.LinkerFieldManager)))
	if col.State.Status != fleet.SourceAvailable || len(col.Targets) != 1 {
		t.Errorf("k8s: state %+v targets %+v", col.State, col.Targets)
	}
}

// TestLinkedSourceAbsentKeyIsUnavailable: a ConfigMap whose linker wrote
// no linked key, or no file, is unavailable and says the operator wrote none
// yet.
func TestLinkedSourceAbsentKeyIsUnavailable(t *testing.T) {
	cm := detectionConfigMap(detectionJSON(t, detectionNow, detect.Complete), true, detect.FieldManager)
	cm.SetManagedFields(append(cm.GetManagedFields(), metav1.ManagedFieldsEntry{Manager: delivery.LinkerFieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:metadata":{}}`)}))
	col := collectLinked(t, k8sLinked(cm))
	assertLinkedUnavailable(t, col, "NOT_FOUND")
	if !strings.Contains(col.State.Error.Message, "no linked workloads yet") {
		t.Errorf("message = %q", col.State.Error.Message)
	}
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFileSource("linked", filepath.Join(t.TempDir(), "linked.json"), clockAt(detectionNow))), "NOT_FOUND")
}

// TestLinkedSourcePartialIsPartial: a partial linker run, and its
// limitations, make the source partial and name it.
func TestLinkedSourcePartialIsPartial(t *testing.T) {
	lim := fleet.Limitation{Code: "LINK_INDEX_PARTIAL", Message: "12 index entries are not read yet"}
	col := collectLinked(t, NewLinkedFileSource("linked", writeLinked(t, linkedJSON(t, detectionNow, fleet.CompletenessPartial, lim)), clockAt(detectionNow)))
	if col.State.Status != fleet.SourcePartial || len(col.Limitations) != 1 || col.Limitations[0].Source != "linked" {
		t.Errorf("state %+v limitations %+v", col.State, col.Limitations)
	}
}

// TestLinkedSourceStaleIsStale: linked targets four minutes old read stale.
func TestLinkedSourceStaleIsStale(t *testing.T) {
	col := collectLinked(t, NewLinkedFileSource("linked", writeLinked(t, linkedJSON(t, detectionNow.Add(-4*time.Minute), fleet.CompletenessComplete)), clockAt(detectionNow)))
	if col.State.Status != fleet.SourceStale {
		t.Errorf("status = %s, want stale", col.State.Status)
	}
}

// TestLinkedSourceForeignConfigMapRefused: a ConfigMap with no operator label
// is refused.
func TestLinkedSourceForeignConfigMapRefused(t *testing.T) {
	cm := operatorConfigMap(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete), detect.FieldManager, delivery.LinkerFieldManager)
	cm.SetLabels(nil)
	assertLinkedUnavailable(t, collectLinked(t, k8sLinked(cm)), "UNTRUSTED")
}

// TestLinkedSourceKeyOwnedByAnotherManagerRefused: detection owns its key and
// a third manager owns linked.json: the linked targets are refused.
func TestLinkedSourceKeyOwnedByAnotherManagerRefused(t *testing.T) {
	cm := operatorConfigMap(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete), detect.FieldManager, "kubectl-patch")
	assertLinkedUnavailable(t, collectLinked(t, k8sLinked(cm)), "UNTRUSTED")
	// Detection's own manager owning only detected.json is no trust either.
	cm = operatorConfigMap(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete), detect.FieldManager, detect.FieldManager+"-x")
	cm.SetManagedFields(cm.GetManagedFields()[:1])
	assertLinkedUnavailable(t, collectLinked(t, k8sLinked(cm)), "UNTRUSTED")
}

// TestDetectionSourceKeyOwnedByAnotherManagerRefused: the linker owns
// linked.json and another manager owns detected.json: the detection is
// refused, though the linker's key is the operator's.
func TestDetectionSourceKeyOwnedByAnotherManagerRefused(t *testing.T) {
	cm := operatorConfigMap(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete), "kubectl-patch", delivery.LinkerFieldManager)
	cm.SetManagedFields(append(cm.GetManagedFields(), metav1.ManagedFieldsEntry{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:linked.json":{}}}`)}))
	assertUnavailable(t, collectDetection(t, k8sDetection(cm)), "UNTRUSTED")
}

// TestLinkedSourceDigestOffStaysAvailable: a linker with no registry checked
// every signal it has, so the source stays available and its state says
// digest linking is off.
func TestLinkedSourceDigestOffStaysAvailable(t *testing.T) {
	off := fleet.Limitation{Code: delivery.LimitationLinkDigestOff, Message: "no registry"}
	col := collectLinked(t, NewLinkedFileSource("linked", writeLinked(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete, off)), clockAt(detectionNow)))
	if col.State.Status != fleet.SourceAvailable || len(col.Limitations) != 0 || col.State.Error == nil || col.State.Error.Code != delivery.LimitationLinkDigestOff {
		t.Fatalf("state %+v error %+v limitations %+v", col.State, col.State.Error, col.Limitations)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: clockAt(detectionNow)}, NewLinkedFileSource("linked",
		writeLinked(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete, off)), clockAt(detectionNow)))
	if err != nil || snap.Sources[0].Status != fleet.SourceAvailable {
		t.Fatalf("snapshot sources %+v, %v", snap.Sources, err)
	}
}

// TestLinkedSourceInvalidIsUnavailable: an undecodable payload, one dated in
// the future, a client that could not be built or a forbidden read are each
// unavailable with their class.
func TestLinkedSourceInvalidIsUnavailable(t *testing.T) {
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFileSource("linked", writeLinked(t, []byte("{")), clockAt(detectionNow))), "INVALID")
	future := linkedJSON(t, detectionNow.Add(detect.MaxFutureSkew+time.Second), fleet.CompletenessComplete)
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFileSource("linked", writeLinked(t, future), clockAt(detectionNow))), "INVALID")
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFailedSource("linked", fmt.Errorf("%w: x", ErrDetectionNoCluster))), "NO_CLUSTER")
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFailedSource("linked", os.ErrPermission)), "AUTH_FAILED")
	assertLinkedUnavailable(t, collectLinked(t, NewLinkedFailedSource("linked", errors.New("x"))), "UNAVAILABLE")
}

// TestLinkedSourceSharesClusterWithWorkloads: a linked source and a workload
// source reading the same cluster are one cluster, and the linked target
// takes over the inferred workload it runs as.
func TestLinkedSourceSharesClusterWithWorkloads(t *testing.T) {
	cl := &fleet.Cluster{ID: linkedCluster, Name: "prod", NameSource: fleet.ClusterNameFromContext}
	deploy := strings.Replace(workloadJSON("shop", "payments", "ghcr.io/acme/payments:2.3.0"), `"namespace":"shop"`,
		`"namespace":"shop","creationTimestamp":"`+detectionNow.Add(-24*time.Hour).Format(time.RFC3339)+`"`, 1)
	workloads := NewWorkloadSource("k8s-workloads", cl, &workloadFake{pages: map[string][2]string{"deployments||": {deploy}}}, nil)
	linked := NewLinkedFileSource("linked", writeLinked(t, linkedJSON(t, detectionNow, fleet.CompletenessComplete)), clockAt(detectionNow))
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: clockAt(detectionNow)}, linked, workloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Clusters) != 1 {
		t.Errorf("clusters = %+v, want one", snap.Clusters)
	}
	for k, s := range snap.Services {
		if s.Inferred() {
			t.Errorf("inferred service %s survived the hand-over", k)
		}
	}
	if len(snap.Replacements) != 1 || snap.Replacements[0].Link != "observed" || !strings.Contains(string(snap.Replacements[0].From), "payments") {
		t.Fatalf("replacements = %+v", snap.Replacements)
	}
}

// TestLinkedSourceNoLinkerIsAdvisory: an older operator's ConfigMap, which
// only the detection manager wrote, means no linker: an advisory source that
// changes no answer, as the file form adds none without linked.json.
func TestLinkedSourceNoLinkerIsAdvisory(t *testing.T) {
	col, err := k8sLinked(detectionConfigMap(detectionJSON(t, detectionNow, detect.Complete), true, detect.FieldManager)).Collect(context.Background())
	if err != nil || !col.Advisory || col.State.Status != fleet.SourceUnavailable || col.State.Error == nil || col.State.Error.Code != "NO_LINKER" {
		t.Fatalf("Collect = %+v %+v, %v; want advisory NO_LINKER", col, col.State, err)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: clockAt(detectionNow)},
		k8sLinked(detectionConfigMap(detectionJSON(t, detectionNow, detect.Complete), true, detect.FieldManager)))
	if err != nil || snap.Completeness == fleet.CompletenessPartial {
		t.Fatalf("completeness %v, %v", snap.Completeness, err)
	}
}
