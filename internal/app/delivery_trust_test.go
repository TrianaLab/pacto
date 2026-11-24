package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

var (
	trustPayments = delivery.ServiceRef{Name: "payments"}
	trustCI       = delivery.PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com",
		Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/dev", Repository: "https://github.com/acme/payments", CI: true}
)

// trustStage is a Stage of service that watches the registry.
func trustStage(name, service string, p *delivery.PublisherStatus) delivery.StageObject {
	st := delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: name}}
	st.Spec.Service, st.Spec.Candidates.Registry, st.Status.Publisher = delivery.ServiceRef{Name: service}, &delivery.RegistryCandidates{}, p
	return st
}

// publishing points e's client at a delivery namespace whose ConfigMap
// publishes cfg (nil: no publishers.json) and e's approver signers.
func (e *atEnv) publishing(cfg *delivery.PublisherConfig, extra ...runtime.Object) {
	e.t.Helper()
	policy, _ := json.Marshal(e.d.Policy)
	data := map[string]any{delivery.PolicyKey: string(policy)}
	if cfg != nil {
		raw, _ := json.Marshal(cfg)
		data[delivery.PublishersKey] = string(raw)
	}
	extra = append(extra, obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap, map[string]any{"data": data}))
	cluster, _, _ := deliveryFixture(e.t, "carol", nil, nil, extra...)
	e.d.Dynamic, e.d.PolicyNamespace = cluster.Dynamic, "pacto-delivery"
}

// pinSigner is the one-key trust config of e's recorder key.
func (e *atEnv) pinSigner() string {
	pem, _ := delivery.PublicKeyPEM(e.key.Public())
	return pem
}

// pin pushes a pin of svc for trustCI, signed by e's recorder key and citing
// the approval at approval, by approver, of the trust operation uid.
func (e *atEnv) pin(svc delivery.ServiceRef, approver, approval, uid, at string) {
	e.t.Helper()
	e.pinIn("prod", svc, approver, approval, uid, at)
}

// pinIn pushes pin as pin does, confirmed on a Stage of namespace ns.
func (e *atEnv) pinIn(ns string, svc delivery.ServiceRef, approver, approval, uid, at string) {
	e.t.Helper()
	repo := e.repo("delivery/streams")
	if err := ocireferrer.Push(context.Background(), repo, deliveryrecord.Anchor(delivery.PinStream(svc))); err != nil {
		e.t.Fatal(err)
	}
	p := delivery.IdentityPin{Service: svc, Namespace: ns, Issuer: trustCI.Issuer, Subject: trustCI.Subject, Repository: trustCI.Repository,
		Approver: approver, Approval: approval, Operation: uid, At: at}
	b, _, err := delivery.SignPin(context.Background(), edSigner(e.key), delivery.NewPinStatement(p))
	if err == nil {
		_, err = deliveryrecord.PushPin(context.Background(), repo, e.mode, b)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

// trustApproval is an approval of the trust operation of trustCI, uid
// uid: recorder-signed on alice's behalf when san is empty, else signed
// keyless by san.
func (e *atEnv) trustApproval(uid, san string) string {
	return e.trustApprovalIn(atStage, uid, san)
}

// trustApprovalIn is trustApproval of the trust operation on stage.
func (e *atEnv) trustApprovalIn(stage delivery.RecordStage, uid, san string) string {
	op := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: delivery.TrustOperationName("payments-prod", trustCI), UID: uid}}
	signer := delivery.ApprovalSigner{Kind: delivery.SignerRecorder, OnBehalfOf: &delivery.ApprovalBehalf{User: "alice"}}
	s, path := edSigner(e.key), "delivery/streams"
	if san != "" {
		signer, s, path = delivery.ApprovalSigner{Kind: delivery.SignerPerson}, e.keyless(san), atApprovals
	}
	st := delivery.NewApprovalRecord(stage, e.anchor.Digest.String(), op, delivery.ApprovalSpec{Decision: delivery.Approve}, signer, "2026-11-18T00:00:00Z")
	ref, _ := e.push(path, s, delivery.PredicateApproval, st)
	return ref
}

func TestTrustDerivedRule(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme"})
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, nil)
	if v.Completeness != "complete" || v.AsOf == "" || v.RuleSource != delivery.PublisherSourceRule || v.Rule == nil ||
		!strings.Contains(v.Rule.Subject, "acme") || len(v.Services) != 1 || len(v.Services[0].Pins) != 0 {
		t.Fatalf("%+v", v)
	}
	// A chart from before delivery.publishers publishes none: no rule, and
	// nothing was left unread.
	e.publishing(nil)
	if v := e.d.PublisherTrust(context.Background(), nil, nil); v.Completeness != "complete" || v.RuleSource != delivery.PublisherSourceNone || v.NoRule == "" {
		t.Fatalf("no publishers.json: %+v", v)
	}
}

func TestTrustPolicyState(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme", Policy: "ghcr.io/acme/policy:v1"})
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments",
		&delivery.PublisherStatus{Source: delivery.PublisherSourcePolicy, PolicyDigest: "sha256:p", PolicyAt: "2026-11-17T00:00:00Z"})}, nil)
	if v.RuleSource != delivery.PublisherSourcePolicy || v.Rule != nil || v.Policy == nil || v.Policy.Digest != "sha256:p" || v.NoRule != "" {
		t.Fatalf("%+v", v)
	}
}

// TestTrustPinsVerified: a pin under the recorder's key citing an approval
// that verifies is listed, whether the recorder signed the approval for a
// person or the person signed it keyless; one a workload signed is a
// proposal, so its pin is named unreadable and never listed.
func TestTrustPinsVerified(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme", StreamsRepository: e.host + "/delivery/streams", PinSigner: e.pinSigner()})
	e.pin(trustPayments, "alice", e.trustApproval("u1", ""), "u1", "2026-11-17T00:00:00Z")
	e.pin(trustPayments, "oidc:alice@acme.example", e.trustApproval("u2", "alice@acme.example"), "u2", "2026-11-18T00:00:00Z")
	e.pin(trustPayments, "alice", e.trustApproval("u3", "https://kubernetes.io/namespaces/agents/serviceaccounts/bot"), "u3", "2026-11-19T00:00:00Z")
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, nil)
	pins := v.Services[0].Pins
	if len(pins) != 2 || pins[0].Operation != "u2" || pins[1].Operation != "u1" || v.Completeness != "partial" ||
		len(v.Unreadable) != 1 || !strings.Contains(v.Unreadable[0], "proposal") {
		t.Fatalf("%+v", v)
	}
}

func TestTrustPinsPartialOnReadFailure(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	stages := []delivery.StageObject{trustStage("payments-prod", "payments", nil)}
	streams := e.host + "/delivery/streams"
	for want, set := range map[string]func(){
		"no delivery namespace":    func() { e.d.PolicyNamespace = "" },
		"not found":                func() { e.d.PolicyNamespace = "elsewhere" },
		"publisher trust config: ": func() { e.publishingRaw("{") },
		"names no pin signer":      func() { e.publishing(&delivery.PublisherConfig{StreamsRepository: streams}) },
		"trust config":             func() { e.publishing(&delivery.PublisherConfig{StreamsRepository: streams, PinSigner: "keys: nope"}) },
		"reads no registry": func() {
			e.publishing(&delivery.PublisherConfig{StreamsRepository: streams, PinSigner: e.pinSigner()})
			e.d.Records = nil
		},
		"pins of payments": func() {
			e.publishing(&delivery.PublisherConfig{StreamsRepository: "UPPER/bad", PinSigner: e.pinSigner()})
		},
		"pins of payments in prod: records down": func() {
			e.publishing(&delivery.PublisherConfig{StreamsRepository: streams, PinSigner: e.pinSigner()})
			e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("records down") }
		},
	} {
		records := e.d.Records
		set()
		v := e.d.PublisherTrust(context.Background(), stages, nil)
		if v.Completeness != "partial" || !strings.Contains(strings.Join(v.Unreadable, "; "), want) {
			t.Errorf("%s: %+v", want, v)
		}
		// Pins never read are unknown (null), never an empty list.
		if v.Services[0].Pins != nil {
			t.Errorf("%s: unread pins answered %+v", want, v.Services[0].Pins)
		}
		e.d.Records = records
	}
	// A config that could not be read states no rule: unknown, and why.
	for _, set := range []func(){func() { e.d.PolicyNamespace = "" }, func() { e.d.PolicyNamespace = "elsewhere" }, func() { e.publishingRaw("{") }} {
		set()
		v := e.d.PublisherTrust(context.Background(), stages, nil)
		if v.RuleSource != delivery.PublisherSourceUnknown || v.Rule != nil || v.Policy != nil ||
			!strings.HasPrefix(v.NoRule, "the publisher trust config could not be read, so the rule is unknown: ") || strings.Contains(v.NoRule, "no publisher registry is configured") {
			t.Errorf("unreadable config: %+v", v)
		}
	}
	// The approver signers cannot be read: a person's approval verifies
	// nothing, so its pin is unreadable.
	e.publishing(&delivery.PublisherConfig{StreamsRepository: streams, PinSigner: e.pinSigner()})
	e.pin(trustPayments, "oidc:alice@acme.example", e.trustApproval("u1", "alice@acme.example"), "u1", "2026-11-17T00:00:00Z")
	cluster, _, _ := deliveryFixture(t, "carol", nil, nil, obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap, map[string]any{"data": map[string]any{
		delivery.PolicyKey: "{", delivery.PublishersKey: `{"streamsRepository":"` + streams + `","pinSigner":` + quote(e.pinSigner()) + `}`}}))
	e.d.Dynamic = cluster.Dynamic
	v := e.d.PublisherTrust(context.Background(), stages, nil)
	if len(v.Services[0].Pins) != 0 || !strings.Contains(strings.Join(v.Unreadable, "; "), "approver signers could not be read") {
		t.Fatalf("unreadable signers: %+v", v)
	}
}

// publishingRaw publishes raw as publishers.json.
func (e *atEnv) publishingRaw(raw string) {
	cluster, _, _ := deliveryFixture(e.t, "carol", nil, nil, obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap,
		map[string]any{"data": map[string]any{delivery.PublishersKey: raw}}))
	e.d.Dynamic, e.d.PolicyNamespace = cluster.Dynamic, "pacto-delivery"
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestTrustPendingFromHeldOperations(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme"})
	id := trustCI
	op := delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: "prod", Name: delivery.TrustOperationName("payments-prod", id)},
		Spec: delivery.OperationSpec{Type: delivery.TypeTrust, Stage: "payments-prod", Reason: "new signer " + id.Subject,
			Params: delivery.Params{To: "sha256:b", Publisher: &id}}, Status: delivery.OperationStatus{Phase: delivery.PhaseHeld}}
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, []delivery.OperationObject{op})
	p := v.Services[0].Pending
	if len(p) != 1 || p[0].Operation != op.Metadata.Name || p[0].Digest != "sha256:b" || p[0].Identity.Subject != id.Subject {
		t.Fatalf("%+v", v)
	}
}

// TestTrustOnlyCallerStages: only the services of the Stages the caller
// listed are named, and only their pins are read: a hidden service's pins,
// approvers and approval ids never reach the answer.
func TestTrustOnlyCallerStages(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{StreamsRepository: e.host + "/delivery/streams", PinSigner: e.pinSigner()})
	hidden := delivery.ServiceRef{Name: "hidden"}
	e.pin(hidden, "alice", e.trustApproval("u9", ""), "u9", "2026-11-17T00:00:00Z")
	var opened []string
	records := e.d.Records
	e.d.Records = func(path string, insecure ...string) (*remote.Repository, error) {
		opened = append(opened, path)
		return records(path, insecure...)
	}
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, nil)
	b, _ := json.Marshal(v)
	if len(v.Services) != 1 || v.Services[0].Service != trustPayments || strings.Contains(string(b), "hidden") || strings.Contains(string(b), "u9") ||
		len(opened) != 1 {
		t.Fatalf("%s %v", b, opened)
	}
	if v := e.d.PublisherTrust(context.Background(), nil, nil); len(v.Services) != 0 || v.Completeness != "complete" {
		t.Fatalf("no Stage listed: %+v", v)
	}
}

// TestTrustPinsOfListedNamespacesOnly: a pin confirmed on a Stage of the same
// service in a namespace the caller listed no Stage in never reaches the
// answer.
func TestTrustPinsOfListedNamespacesOnly(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{StreamsRepository: e.host + "/delivery/streams", PinSigner: e.pinSigner()})
	teamX := atStage
	teamX.Namespace = "team-x"
	e.pinIn("team-x", trustPayments, "alice", e.trustApprovalIn(teamX, "u9", ""), "u9", "2026-11-17T00:00:00Z")
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, nil)
	b, _ := json.Marshal(v)
	if len(v.Services) != 1 || len(v.Services[0].Pins) != 0 || strings.Contains(string(b), "u9") || v.Completeness != "complete" {
		t.Fatalf("%s", b)
	}
	// team-x's own Stage reads it.
	other := trustStage("payments-x", "payments", nil)
	other.Metadata.Namespace = "team-x"
	if v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{other}, nil); len(v.Services[0].Pins) != 1 || v.Services[0].Pins[0].Namespace != "team-x" {
		t.Fatalf("team-x: %+v", v)
	}
}

// TestTrustPinsCutPartial: more pins than a listing reads make the answer
// partial and say so, in the trust view and to fleet provenance.
func TestTrustPinsCutPartial(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.publishing(&delivery.PublisherConfig{StreamsRepository: e.host + "/delivery/streams", PinSigner: e.pinSigner()})
	approval := e.trustApproval("u1", "")
	for i := range deliveryrecord.MaxPins + 1 {
		e.pin(trustPayments, "alice", approval, "u1", fmt.Sprintf("2026-11-17T00:00:%02dZ", i))
	}
	v := e.d.PublisherTrust(context.Background(), []delivery.StageObject{trustStage("payments-prod", "payments", nil)}, nil)
	if v.Completeness != "partial" || !strings.Contains(strings.Join(v.Unreadable, "; "), "more than 20 pins of payments are attached and only 20 were read") {
		t.Fatalf("%+v", v)
	}
	in, _ := e.d.InstallationTrust(context.Background())
	if _, note, _, err := in.Pins(context.Background(), trustPayments); err != nil || !strings.Contains(note, "only 20 were read") {
		t.Fatalf("fleet: %q %v", note, err)
	}
}
