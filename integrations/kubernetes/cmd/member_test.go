package main

import (
	"crypto/ed25519"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/controller"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/inferredpublish"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/member"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// fakeManager is the slice of ctrl.Manager the installation wiring uses.
type fakeManager struct {
	ctrl.Manager
	c     client.Client
	added []manager.Runnable
}

func newFakeManager() *fakeManager {
	return &fakeManager{c: fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()}
}

func (m *fakeManager) Add(r manager.Runnable) error { m.added = append(m.added, r); return nil }
func (m *fakeManager) GetClient() client.Client     { return m.c }
func (m *fakeManager) GetAPIReader() client.Reader  { return m.c }
func (m *fakeManager) GetConfig() *rest.Config {
	return &rest.Config{Host: "https://member.example:6443"}
}
func (m *fakeManager) GetScheme() *runtime.Scheme { return clientgoscheme.Scheme }

func (m *fakeManager) has(match func(manager.Runnable) bool) bool {
	return slices.ContainsFunc(m.added, match)
}

func isLink(r manager.Runnable) bool { _, ok := r.(*member.Link); return ok }

func isInferred(r manager.Runnable) bool { _, ok := r.(*inferredpublish.InferredPublisher); return ok }

var testIdent = kubecluster.Identity{ID: "uid-1", Name: "eu-prod"}

func TestAddTargetSnapshotReturnsKey(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	m := newFakeManager()
	inst := &detect.Installation{}
	key, err := addTargetSnapshot(m, &options{registry: "registry.example/acme"}, "pacto", testIdent, nil, nil, inst, nil)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		t.Fatalf("addTargetSnapshot = %d-byte key, %v; want the installation key", len(key), err)
	}
	if !m.has(isInferred) || inst.SnapshotKeyID == "" {
		t.Fatal("a registry install publishes inferred contracts and advertises the snapshot key")
	}
	key, err = addTargetSnapshot(newFakeManager(), &options{}, "pacto", testIdent, nil, nil, &detect.Installation{}, nil)
	if key != nil || err != nil {
		t.Fatalf("addTargetSnapshot without --registry = %v, %v; want no key", key, err)
	}
}

func TestMemberDoesNotPublishInferred(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	m := newFakeManager()
	o := &options{registry: "registry.example/acme", hubURL: "https://pacto.acme.dev", profile: "default"}
	if _, err := addPublishers(m, o, "pacto", testIdent, nil, nil, nil, &controller.PactoReconciler{}); err != nil {
		t.Fatal(err)
	}
	if m.has(isInferred) || !m.has(isLink) {
		t.Fatalf("a member adds its link and publishes no inferred contracts: %T", m.added)
	}
}

func TestAddMemberNoopWithoutHubURL(t *testing.T) {
	m := newFakeManager()
	if err := addMember(m, &options{registry: "registry.example/acme"}, testIdent, nil); err != nil || len(m.added) != 0 {
		t.Fatalf("addMember without --hub-url = %v, %d runnables; want a no-op", err, len(m.added))
	}
}

func TestHubURLNeedsRegistry(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	err := addMember(newFakeManager(), &options{hubURL: "https://pacto.acme.dev"}, testIdent, key)
	if err == nil || !strings.Contains(err.Error(), "--registry") {
		t.Fatalf("addMember without --registry = %v; want refused", err)
	}
	err = addMember(newFakeManager(), &options{hubURL: "http://pacto.acme.dev", registry: "r.example/a"}, testIdent, key)
	if err == nil || !strings.Contains(err.Error(), "--hub-url") {
		t.Fatalf("addMember with an http URL = %v; want refused", err)
	}
	o := &options{hubURL: "https://pacto.acme.dev", registry: "r.example/a"}
	err = addMember(newFakeManager(), o, kubecluster.Identity{}, key)
	if err == nil || !strings.Contains(err.Error(), "cluster id") {
		t.Fatalf("addMember without a cluster id = %v; want refused", err)
	}
}

func TestAddInstallationRefusesBadProfile(t *testing.T) {
	o, rec := &options{profile: "nope"}, &controller.PactoReconciler{}
	if _, err := addPublishers(newFakeManager(), o, "pacto", testIdent, nil, nil, nil, rec); err == nil {
		t.Fatal("an unknown profile stops the operator")
	}
}

// Detection writes the evidence and recorder rows only under profile idp.
func TestIDPPiecesOnlyUnderIDP(t *testing.T) {
	if idpPieces(&detect.Installation{Profile: []string{"default"}}, nil, false, "") != nil {
		t.Fatal("pieces outside idp")
	}
	got := idpPieces(&detect.Installation{Profile: []string{"idp"}}, nil, true, "no-signer")()
	if len(got) != 2 ||
		got[0].Kind != "evidence" || got[0].Value != "on" ||
		got[1].Kind != "recorder" || got[1].Value != "off" {
		t.Fatalf("pieces %+v", got)
	}
}
