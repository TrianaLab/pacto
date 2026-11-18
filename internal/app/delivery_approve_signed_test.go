package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const acmeIssuer = "https://idp.acme.example"

// signedEnv is a held operation on payments-prod whose Stage keeps an
// approvals repository in mode, with a keyless signer configured for acme.
func signedEnv(t *testing.T, mode capabilities.ReferrersMode) *recordEnv {
	t.Helper()
	op := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "p1", map[string]any{
		"spec": map[string]any{"intentDigest": "sha256:i", "stage": "payments-prod", "actor": map[string]any{"user": "bob"}},
		"status": map[string]any{"phase": "Held", "stageUID": "uid-stage", "guarded": true,
			"times": map[string]any{"held": "2020-01-01T00:00:00Z"}},
	})
	e := newRecordEnvOn(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags}, op)
	e.d.Policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: acmeIssuer, UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.example$`}}}
	stream := map[string]any{"id": recStream, "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream),
		"approvals": deliveryrecord.AnchorRef(e.host+"/delivery/approvals", recStream)}
	if mode != "" {
		stream["registryMode"] = string(mode)
	}
	e.setStream(map[string]any{"stream": stream})
	if err := ocireferrer.Push(context.Background(), e.repo("delivery/approvals"), deliveryrecord.Anchor(recStream)); err != nil {
		t.Fatal(err)
	}
	return e
}

// attached lists what is attached to the stream anchor in repository path,
// approval and intent records included.
func (e *recordEnv) attached(path string) int {
	e.t.Helper()
	descs, tags, err := ocireferrer.Attached(context.Background(), e.repo(path), e.anchor, deliveryrecord.AnchorTag(recStream)+"-")
	if err != nil {
		e.t.Fatal(err)
	}
	return len(descs) + len(tags)
}

func TestApproveSignedPushesToApprovalsRepository(t *testing.T) {
	for _, mode := range []capabilities.ReferrersMode{"", capabilities.ReferrersWriteOnceTags} {
		e := signedEnv(t, mode)
		ref, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "ok", edSigner(e.key), acmeIssuer, "alice@acme.example")
		if err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
		if !strings.HasPrefix(ref.Ref, e.host+"/delivery/approvals@sha256:") || !strings.HasPrefix(ref.Digest, "sha256:") {
			t.Errorf("%q: ref %+v", mode, ref)
		}
		if got := e.attached("delivery/approvals"); got != 1 {
			t.Errorf("%q: %d approval records, want 1", mode, got)
		}
		if got := e.attached("delivery/streams"); got != 0 {
			t.Errorf("%q: the operation-record stream got %d records", mode, got)
		}
		repo := e.repo("delivery/approvals")
		_, payload, err := repo.FetchReference(context.Background(), strings.TrimPrefix(ref.Ref, e.host+"/delivery/approvals@"))
		if err != nil {
			t.Fatal(err)
		}
		_ = payload.Close()
		if mode == capabilities.ReferrersWriteOnceTags {
			tags, _ := ocireferrer.Tags(context.Background(), repo, deliveryrecord.AnchorTag(recStream)+"-"+deliveryrecord.KindApproval+"-")
			if len(tags) != 1 {
				t.Errorf("write-once tags: %v", tags)
			}
		}
	}
}

// TestApproveSignedRecordsThePerson: the statement names the person as the
// signer, the operation it answers and the decision.
func TestApproveSignedRecordsThePerson(t *testing.T) {
	e := signedEnv(t, "")
	var payload []byte
	signer := signerFunc(func(_ context.Context, _ string, p []byte) ([]byte, error) {
		payload = p
		return edSigner(e.key).Sign(context.Background(), delivery.PayloadType, p)
	})
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Reject, "no", signer, acmeIssuer, "alice@acme.example"); err != nil {
		t.Fatal(err)
	}
	var st delivery.ApprovalStatement
	_ = json.Unmarshal(payload, &st)
	p := st.Predicate
	if p.Signer.Kind != delivery.SignerPerson || p.Signer.OnBehalfOf != nil || p.Decision != delivery.Reject || p.Note != "no" ||
		p.Operation.UID != "uid-p1" || p.Operation.IntentDigest != "sha256:i" || p.Operation.Stage != (delivery.RecordStage{Cluster: "c", Namespace: "prod", Name: "payments-prod", UID: "uid-stage"}) ||
		st.Subject[0].Digest["sha256"] != e.anchor.Digest.Encoded() {
		t.Errorf("statement %+v", st)
	}
}

type signerFunc func(context.Context, string, []byte) ([]byte, error)

func (f signerFunc) Sign(ctx context.Context, t string, p []byte) ([]byte, error) {
	return f(ctx, t, p)
}

func TestApproveSignedRefusesWithoutApprovalsRepository(t *testing.T) {
	e := signedEnv(t, "")
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream}})
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil ||
		err.Error() != "this Stage keeps no approvals repository: the recorder is off or has not anchored it yet" {
		t.Fatal(err)
	}
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "approvals": deliveryrecord.AnchorRef(e.host+"/x", "other/stream")}})
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("pushed to another stream's anchor")
	}
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "gone", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("approved a missing operation")
	}
	orphan := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", "p2", map[string]any{"spec": map[string]any{"stage": "gone"}})
	if _, err := e.c.Resource(gvrOperations).Namespace("prod").Create(context.Background(), orphan, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p2", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("approved on a missing Stage")
	}
}

func TestApproveSignedRefusesURISigner(t *testing.T) {
	e := signedEnv(t, "")
	_, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "https://github.com/acme/app/.github/workflows/ci.yml@refs/heads/main")
	if err == nil || !strings.HasPrefix(err.Error(), "AgentsMayNotApprove: agents cannot approve") {
		t.Fatal(err)
	}
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), "https://other.example", "alice@acme.example"); err == nil {
		t.Fatal("an unconfigured issuer approved")
	}
	if e.attached("delivery/approvals") != 0 {
		t.Fatal("a refused approval was pushed")
	}
}

func TestApproveSignedPredictsRefusal(t *testing.T) {
	e := signedEnv(t, "")
	// bob asked for the change, so he never decides it.
	e.d.Policy.Signers[0].Subjects = ".+"
	e.d.Policy.Signers[0].UsernamePrefix = ""
	_, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "bob")
	if err == nil || !strings.HasPrefix(err.Error(), "the promoter would not count this decision, so none was signed: bob asked for this change") {
		t.Fatal(err)
	}
	if e.attached("delivery/approvals") != 0 {
		t.Fatal("a refused approval was pushed")
	}
}

func TestApproveSignedFailures(t *testing.T) {
	e := signedEnv(t, "")
	fail := signerFunc(func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("no login") })
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", fail, acmeIssuer, "alice@acme.example"); err == nil || !strings.Contains(err.Error(), "no login") {
		t.Fatal(err)
	}
	e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("bad repo") }
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil || err.Error() != "bad repo" {
		t.Fatal(err)
	}
	e = signedEnv(t, "")
	e.d.PolicyNamespace = "pacto-delivery"
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("approved with an unreadable identity policy")
	}
	e.d.PolicyNamespace = ""
	e.srv.Close()
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("approved on an unreachable registry")
	}
}

// TestApproveSignedProbesTheApprovalsRepository: the approvals repository
// may sit on a registry without Referrers while the stream reads native, so
// the push takes the approvals host's form, never the stream's mode.
func TestApproveSignedProbesTheApprovalsRepository(t *testing.T) {
	e := signedEnv(t, capabilities.ReferrersWriteOnceTags)
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "registryMode": string(capabilities.ReferrersNative),
		"anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream), "approvals": deliveryrecord.AnchorRef(e.host+"/delivery/approvals", recStream)}})
	ref, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "ok", edSigner(e.key), acmeIssuer, "alice@acme.example")
	if err != nil || ref.Mode != capabilities.ReferrersWriteOnceTags {
		t.Fatalf("%+v %v", ref, err)
	}
	tags, _ := ocireferrer.Tags(context.Background(), e.repo("delivery/approvals"), deliveryrecord.AnchorTag(recStream)+"-"+deliveryrecord.KindApproval+"-")
	if len(tags) != 1 {
		t.Fatalf("write-once tags: %v", tags)
	}
}

// TestApproveSignedAsksForTheKeylessIdentity: the promoter checks the
// keyless user with no groups, so a right that comes only through the
// caller's groups is refused before anything is signed.
func TestApproveSignedAsksForTheKeylessIdentity(t *testing.T) {
	e := signedEnv(t, "")
	var asked string
	e.c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		spec, _, _ := unstructured.NestedMap(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec")
		asked = fmt.Sprint(spec["user"], spec["groups"])
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": false}}}, nil
	})
	_, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example")
	if err == nil || !strings.HasPrefix(err.Error(), "the promoter would not count this decision") || asked != "oidc:alice@acme.example<nil>" {
		t.Fatalf("%v (asked %q)", err, asked)
	}
	e.c.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"evaluationError": "webhook down"}}}, nil
	})
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil ||
		!strings.Contains(err.Error(), "could not be evaluated: webhook down") {
		t.Fatal(err)
	}
	// A client that may not ask about another identity falls back to its own
	// review and says what that cannot show.
	e.c.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	ref, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example")
	if err != nil || !strings.Contains(ref.Caveat, "could not ask whether oidc:alice@acme.example itself may update") {
		t.Fatalf("%+v %v", ref, err)
	}
}

func TestApproveSignedUnknownWorkloadKindIsUnverified(t *testing.T) {
	e := signedEnv(t, "")
	stages := e.c.Resource(gvrStages).Namespace("prod")
	st, _ := stages.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	_ = unstructured.SetNestedMap(st.Object, map[string]any{"kind": "Rollout", "name": "payments"}, "spec", "workload")
	if _, err := stages.Update(context.Background(), st, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.ApproveSigned(context.Background(), "prod", "p1", delivery.Approve, "", edSigner(e.key), acmeIssuer, "alice@acme.example"); err == nil ||
		!strings.Contains(err.Error(), "Rollout") {
		t.Fatal(err)
	}
}
