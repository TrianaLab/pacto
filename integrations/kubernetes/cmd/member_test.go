package main

import (
	"crypto/ed25519"
	"flag"
	"io"
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

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/controller"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/inferredpublish"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/member"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/offeringpublish"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/targetsnapshot"
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

func isOfferings(r manager.Runnable) bool { _, ok := r.(*offeringpublish.Publisher); return ok }

var testIdent = kubecluster.Identity{ID: "uid-1", Name: "eu-prod"}

func TestAddTargetSnapshotReturnsKey(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	m := newFakeManager()
	inst := &detect.Installation{}
	o := &options{registry: "registry.example/acme"}
	key, _, err := addTargetSnapshot(m, o, "pacto", testIdent, nil, nil, inst, nil, nil)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		t.Fatalf("addTargetSnapshot = %d-byte key, %v; want the installation key", len(key), err)
	}
	if !m.has(isInferred) || inst.SnapshotKeyID == "" {
		t.Fatal("a registry install publishes inferred contracts and advertises the snapshot key")
	}
	o = &options{crossplaneOfferings: true}
	inst = &detect.Installation{}
	key, offerings, err := addTargetSnapshot(newFakeManager(), o, "pacto", testIdent, nil, nil, inst, nil, nil)
	if key != nil || offerings != nil || err != nil {
		t.Fatalf("addTargetSnapshot without --registry = %v, %v; want no key", key, err)
	}
}

func TestMemberDoesNotPublishInferred(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	m := newFakeManager()
	o := &options{registry: "registry.example/acme", hubURL: "https://pacto.acme.dev", profile: "default"}
	if _, _, err := addPublishers(m, o, "pacto", testIdent, nil, nil, nil, &controller.PactoReconciler{}); err != nil {
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
	if _, _, err := addPublishers(newFakeManager(), o, "pacto", testIdent, nil, nil, nil, rec); err == nil {
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

// TestChartVersionFlagAdvertised: --chart-version reaches the installation
// block detection advertises, the version a generated member install pins.
func TestChartVersionFlagAdvertised(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	o := &options{}
	bindFlags(fs, o)
	if err := fs.Parse([]string{"--chart-version=0.42.0", "--profile=default"}); err != nil {
		t.Fatal(err)
	}
	inst, _, err := addPublishers(newFakeManager(), o, "pacto", testIdent, nil, nil, nil, &controller.PactoReconciler{})
	if err != nil || inst.ChartVersion != "0.42.0" {
		t.Fatalf("installation = %+v, %v", inst, err)
	}
}

// TestClusterInventoryFlagsParse: the inventory flags parse into the
// Detector's config, and a namespace that is not a DNS label stops the
// operator at start.
func TestClusterInventoryFlagsParse(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	o := &options{}
	bindFlags(fs, o)
	if err := fs.Parse([]string{"--cluster-inventory", "--cluster-inventory-namespaces=argocd,flux-system"}); err != nil {
		t.Fatal(err)
	}
	if !o.inventory.Enabled || !slices.Equal(o.inventory.SecretNamespaces, []string{"argocd", "flux-system"}) {
		t.Fatalf("inventory = %+v", o.inventory)
	}
	fs = flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindFlags(fs, &options{})
	err := fs.Parse([]string{"--cluster-inventory-namespaces=argocd,Flux_System"})
	if err == nil || !strings.Contains(err.Error(), `"Flux_System" is not a namespace name`) {
		t.Fatalf("a bad namespace = %v", err)
	}
}

const testReg = "registry.example/acme"

func TestAddTargetSnapshotRunsOfferings(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	for _, tc := range []struct {
		name string
		o    options
		want bool
	}{
		{"on", options{registry: testReg, crossplaneOfferings: true}, true},
		{"member", options{registry: testReg, crossplaneOfferings: true, hubURL: "https://pacto.acme.dev"}, true},
		{"off", options{registry: testReg}, false},
		{"scoped", options{registry: testReg, crossplaneOfferings: true, namespaces: []string{"team-a"}}, false},
	} {
		m := newFakeManager()
		_, offerings, err := addTargetSnapshot(m, &tc.o, "pacto", testIdent, nil, nil, &detect.Installation{}, nil, nil)
		if err != nil || m.has(isOfferings) != tc.want || (offerings != nil) != tc.want {
			t.Fatalf("%s: offerings = %v, added %T, err %v", tc.name, offerings != nil, m.added, err)
		}
		if tc.want && offerings().Repository != "registry.example/acme/pacto-inferred/crossplane-uid-1/services" {
			t.Fatalf("%s: advertisement = %+v", tc.name, offerings())
		}
	}
}

// TestPublisherJoinRequestsFollowIntake: the target publisher asks the
// running intake what its join block says about requests, and nothing runs
// or answers while --member-requests is off.
func TestPublisherJoinRequestsFollowIntake(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	for _, on := range []bool{false, true} {
		m := newFakeManager()
		o := &options{registry: "registry.example/acme", profile: "default", memberRequests: on,
			deliveryPolicyConfigMap: "pacto-delivery/pacto-delivery-identities"}
		if _, _, err := addPublishers(m, o, "pacto", testIdent, nil, nil, nil, &controller.PactoReconciler{}); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(m.added, func(r manager.Runnable) bool {
			_, ok := r.(*targetsnapshot.TargetPublisher)
			return ok
		})
		if i < 0 || m.has(isIntake) != on {
			t.Fatalf("member requests %v: want the publisher and an intake only when on, got %T", on, m.added)
		}
		if pub := m.added[i].(*targetsnapshot.TargetPublisher); pub.Requests == nil || pub.Requests() != nil {
			t.Fatalf("member requests %v: the publisher asks the intake, which answers nothing before it read a policy", on)
		}
	}
}

// TestInferredReadsDetectorInProcess: the inferred publisher reads the
// detector's last Result through lastDetected, never the ConfigMap.
func TestInferredReadsDetectorInProcess(t *testing.T) {
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	res := &detect.Result{Cluster: "uid-1"}
	lastDetected = func() *detect.Result { return res }
	t.Cleanup(func() { lastDetected = nil })
	m := newFakeManager()
	o := &options{registry: "registry.example/acme"}
	if _, _, err := addTargetSnapshot(m, o, "pacto", testIdent, nil, nil, &detect.Installation{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(m.added, isInferred)
	if p := m.added[i].(*inferredpublish.InferredPublisher); p.Detected == nil || p.Detected() != res {
		t.Fatal("the inferred publisher does not read the detector's last Result")
	}
}

func isIntake(r manager.Runnable) bool { _, ok := r.(*member.Intake); return ok }

func TestAddIntakeRefusals(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	cases := map[string]struct {
		o    options
		key  ed25519.PrivateKey
		want string
	}{
		"no registry key": {options{memberRequests: true}, nil, "needs --registry"},
		"no policy map": {options{memberRequests: true, registry: "registry.example/acme"}, key,
			"--delivery-policy-configmap"},
		"bad registry name": {options{memberRequests: true, registry: "Not A Registry", deliveryPolicyConfigMap: "a/b"}, key,
			"member request repository"},
	}
	for name, tc := range cases {
		in, err := addIntake(newFakeManager(), &tc.o, testIdent, tc.key, nil)
		if in != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: addIntake = %v, %v; want an error naming %q", name, in, err, tc.want)
		}
	}
	t.Setenv("OPERATOR_DEPLOYMENT_NAME", "pacto-operator")
	o := &options{registry: "registry.example/acme", profile: "default", memberRequests: true}
	_, _, err := addPublishers(newFakeManager(), o, "pacto", testIdent, nil, nil, nil, &controller.PactoReconciler{})
	if err == nil {
		t.Fatal("an intake that cannot start stops the operator")
	}
}
