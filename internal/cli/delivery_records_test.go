package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

const cliStream = "c/prod/payments-prod"

// writeTrust writes pub as a trust file and returns its path.
func writeTrust(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	pemKey, _ := delivery.PublicKeyPEM(pub)
	p := filepath.Join(t.TempDir(), "recorder.pub")
	if err := os.WriteFile(p, []byte(pemKey), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// recordDeps is a cluster whose Stage runs A, moved there by "ship", whose
// record is signed by key and sits on the Stage's anchor in an in-process
// registry.
func recordDeps(t *testing.T, key ed25519.PrivateKey) deps {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	streams := strings.TrimPrefix(srv.URL, "http://") + "/delivery/streams"
	repo, _ := ocireferrer.Open(streams, ocireferrer.Options{})
	anchor := deliveryrecord.Anchor(cliStream)
	ctx := context.Background()
	if err := ocireferrer.Push(ctx, repo, anchor); err != nil {
		t.Fatal(err)
	}
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation, Predicate: delivery.RecordPredicate{
		Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: delivery.TypePromote},
		Intent:    delivery.RecordIntent{UID: "uid-ship", Name: "ship", Reason: "ship it", Params: &delivery.Params{To: cliA}},
		Actor:     &delivery.RecordActor{User: "bot", Class: delivery.ClassAgent, OnBehalfOf: &delivery.OnBehalfOf{User: "carol", Asserted: true}},
		Approvals: []delivery.RecordApproval{{By: "alice", Decision: delivery.Approve, Note: "fine"}},
		Verdicts:  []delivery.Verdict{{Gate: delivery.GateContract, Result: delivery.Pass, Message: "compliant"}},
		Outcome:   &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified, Pointer: delivery.PointerChange{After: cliA}},
		Chain:     delivery.Chain{Stream: cliStream, Seq: 1}, Time: delivery.RecordTime{Finished: "2026-10-21T10:00:00Z"}}}
	b, id, err := delivery.SignRecord(ctx, delivery.Ed25519Signer(key), st)
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, b, &anchor.ManifestDesc,
		map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation})
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		t.Fatal(err)
	}
	d := defaultDeps()
	d.newDeliveryRegistry = func(...string) app.DeliveryRegistry { return cliRegistry{} }
	d.newRecordRepo = func(path string, insecure ...string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Insecure: insecure})
	}
	d.newDynamic = func() (dynamic.Interface, error) {
		lists := map[schema.GroupVersionResource]string{
			{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}: "OperationList",
			{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}:     "StageList",
			{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "pactos"}:              "PactoList",
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}:   "KustomizationList",
		}
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists,
			u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-prod", map[string]any{
				"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"}},
				"status": map[string]any{"pointer": cliA, "lastOp": "uid-ship", "stream": map[string]any{"id": cliStream, "seq": int64(1),
					"recordedSeq": int64(1), "head": id, "anchor": deliveryrecord.AnchorRef(streams, cliStream)}}}),
			u("source.toolkit.fluxcd.io/v1", "OCIRepository", "payments", map[string]any{"spec": map[string]any{"url": "oci://reg/apps/payments"}}),
		)
		c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": "alice"}}}}, nil
		})
		return c, nil
	}
	return d
}

func TestDeliveryRecordVerbs(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	trust := writeTrust(t, pub)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	wrong := writeTrust(t, other)
	d := recordDeps(t, key)
	stage := []string{"-n", "prod", "--stage", "payments-prod"}
	checkDeliveryRuns(t, d, []deliveryRun{
		{"text", append([]string{"history", "--trust", trust}, stage...), false, []string{"ship", "sha256:", "from records, complete",
			"verified with trust config"}},
		{"text", append([]string{"history"}, stage...), false, []string{"from records, partial", "no signature was checked"}},
		{"text", append([]string{"history", "--trust", wrong}, stage...), false, []string{"unreadable: sha256:", "problem: the head", "from records, partial"}},
		{"text", append([]string{"blame", "--trust", trust}, stage...), false, []string{"version:   " + cliA, "change:    seq 1 ship (promote)",
			"asked by:  bot (agent)", "for:       carol (asserted)", "why:       ship it", "decided:   alice approve fine",
			"pacto.contract", "from records, complete"}},
		{"json", append([]string{"blame", "--trust", trust}, stage...), false, []string{`"onBehalfOf": "carol"`}},
		{"text", append([]string{"record", "verify", "--trust", trust}, stage...), false, []string{"seq 1", "ship", "cosign verify-blob-attestation"}},
		{"text", append([]string{"record", "verify", "--trust", wrong}, stage...), true, []string{"do not verify"}},
		{"text", append([]string{"record", "verify"}, stage...), true, []string{"needs a trust config"}},
		{"text", []string{"record", "verify", "-n", "prod", "--stage", "missing", "--trust", trust}, true, nil},
		{"json", append([]string{"record", "verify", "--trust", trust}, stage...), false, []string{`"ok": true`}},
		{"text", []string{"undo", "-n", "prod", "ship", "--stage", "payments-prod", "--trust", trust}, false, []string{"undo prod/ship: none",
			"first version"}},
		{"text", []string{"undo", "-n", "prod", "ship"}, true, []string{"name its Stage"}},
		{"text", append([]string{"history", "--trust", filepath.Join(t.TempDir(), "absent")}, stage...), true, []string{"reading the delivery trust config"}},
	})
	t.Setenv(trustEnv, trust)
	checkDeliveryRuns(t, d, []deliveryRun{{"text", append([]string{"history"}, stage...), false, []string{"from records, complete"}}})
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("keys: ["), 0o600)
	checkDeliveryRuns(t, d, []deliveryRun{{"text", append([]string{"history", "--trust", bad}, stage...), true, []string{"bad.yaml"}}})
}

func TestDeliveryUndoPrintsThePlanAndProposesNothing(t *testing.T) {
	d := deliveryDeps("alice", nil)
	inner := d.newDynamic
	d.newDynamic = func() (dynamic.Interface, error) {
		c, _ := inner()
		fc := c.(*dynamicfake.FakeDynamicClient)
		_, _ = fc.Resource(schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}).
			Namespace("prod").Create(context.Background(), u("delivery.pacto.trianalab.io/v1alpha1", "Operation", "older", map[string]any{
			"spec":   map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "params": map[string]any{"from": cliB, "to": cliC}},
			"status": map[string]any{"phase": "Concluded", "pointer": map[string]any{"before": cliB, "after": cliC}}}), metav1.CreateOptions{})
		return fc, nil
	}
	checkDeliveryRuns(t, d, []deliveryRun{
		{"text", []string{"undo", "-n", "prod", "older"}, false, []string{"undo prod/older: promote", "intent:", "nothing was proposed",
			"pacto delivery propose -n prod --stage payments-prod --type promote --to " + cliB}},
		{"text", []string{"undo", "-n", "prod", "held"}, false, []string{"moved nothing"}},
		{"text", []string{"blame", "-n", "prod", "--stage", "payments-prod"}, false, []string{"unknown, not nobody"}},
		{"text", []string{"blame", "-n", "prod", "--stage", "missing"}, true, nil},
		{"text", []string{"undo", "-n", "prod", "missing", "--stage", "missing"}, true, nil},
	})
	if shortRecord("") != "-" {
		t.Fatal("a line read from status needs a dash")
	}
	out := &strings.Builder{}
	printPrecedent(out, &delivery.Precedent{Completeness: "complete", Note: "n",
		Entries: []delivery.PrecedentEntry{{Stream: "c/test/s", Seq: 2, Type: delivery.TypePromote, Outcome: delivery.Failed, Reason: "VerifyFailed"}}})
	if !strings.Contains(out.String(), "precedent: c/test/s seq 2 promote Failed VerifyFailed") || !strings.Contains(out.String(), "precedent: complete: n") {
		t.Fatal(out.String())
	}
	if r, err := newRecordRepo("reg.example/x"); err != nil || r == nil {
		t.Fatalf("record repository: %v", err)
	}
}
