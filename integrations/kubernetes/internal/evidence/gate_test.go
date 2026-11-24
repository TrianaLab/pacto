/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const gateURL = "http://pacto-evidence.test-ns.svc:8686"

func trustSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "trust"}, Data: data}
}

func keyBytes() map[string][]byte {
	return map[string][]byte{"ci.pub": []byte("KEYBYTES-ci"), "adapter.pub": []byte("KEYBYTES-adapter")}
}

func native(context.Context) (capabilities.Capabilities, error) {
	return capabilities.Capabilities{Referrers: capabilities.ReferrersNative}, nil
}

func pushed() *detect.PushResult {
	return &detect.PushResult{At: time.Now(), Ref: "zot.acme.internal/acme/pacto-targets:cluster-x"}
}

// newGate is a gate whose every prerequisite holds, reading objs.
func newGate(objs ...client.Object) *Gate {
	return &Gate{Reader: fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build(),
		Namespace: "test-ns", TrustSecret: "trust", Registry: "zot.acme.internal/acme", Subjects: []string{"oci://zot.acme.internal/acme/"},
		Probe: native, LastPush: pushed, URL: gateURL}
}

func check(t *testing.T, g *Gate) (bool, bool, detect.Entry) {
	t.Helper()
	return g.Check(context.Background())
}

func wantOff(t *testing.T, g *Gate, why string) detect.Entry {
	t.Helper()
	on, known, e := check(t, g)
	if on || !known || e.Value != "off" || e.Completeness != detect.Complete || e.Evidence != why {
		t.Fatalf("on=%v known=%v entry=%+v", on, known, e)
	}
	return e
}

func wantUnknown(t *testing.T, g *Gate, reason string) {
	t.Helper()
	on, known, e := check(t, g)
	if on || known || e.Value != "unknown" || e.Completeness != detect.Unknown || !strings.Contains(e.Reason, reason) {
		t.Fatalf("on=%v known=%v entry=%+v", on, known, e)
	}
}

func TestGateOnWithReferrersAndKey(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	on, known, e := check(t, g)
	if !on || !known || e.Kind != "evidence" || e.Value != "on" || e.Completeness != detect.Complete ||
		e.Evidence != "zot.acme.internal serves OCI Referrers; the trust Secret holds 2 public keys" {
		t.Fatalf("on=%v known=%v entry=%+v", on, known, e)
	}
	if len(e.Objects) != 1 || e.Objects[0] != (detect.Object{Kind: "Secret", Namespace: "test-ns", Name: "trust"}) {
		t.Fatalf("objects %+v", e.Objects)
	}
	g = newGate(trustSecret(map[string][]byte{"ci.pub": []byte("k"), "empty.pub": nil}))
	if _, _, e := check(t, g); !strings.HasSuffix(e.Evidence, "holds 1 public key") {
		t.Fatalf("entry %+v", e)
	}
}

func TestGateOffWithoutRegistry(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Registry = ""
	wantOff(t, g, "no registry value")
}

func TestGateOffWithoutReferrers(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Probe = func(context.Context) (capabilities.Capabilities, error) {
		return capabilities.Capabilities{Referrers: capabilities.ReferrersWriteOnceTags}, nil
	}
	wantOff(t, g, "zot.acme.internal does not serve OCI Referrers")
}

func TestGateOffWithEmptyTrustSecret(t *testing.T) {
	e := wantOff(t, newGate(trustSecret(map[string][]byte{"README": []byte("x")})), "the trust Secret holds no public key")
	if len(e.Objects) != 1 {
		t.Fatalf("objects %+v", e.Objects)
	}
	wantOff(t, newGate(), "the trust Secret does not exist")
}

func TestGateCountsConfigKeyAsTrust(t *testing.T) {
	g := newGate(trustSecret(map[string][]byte{"trust.yaml": []byte("x")}))
	wantOff(t, g, "the trust Secret holds no public key")
	g.TrustConfigKey = "trust.yaml"
	if on, _, e := check(t, g); !on || !strings.HasSuffix(e.Evidence, "holds a trust config") {
		t.Fatalf("on=%v entry=%+v", on, e)
	}
}

// With a config key the server reads only that file: .pub keys beside it
// never turn the gate on, and an empty config is no trust.
func TestGateWithConfigKeyIgnoresPubKeys(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.TrustConfigKey = "trust.yaml"
	wantOff(t, g, "the trust Secret holds no trust.yaml")
	g = newGate(trustSecret(map[string][]byte{"trust.yaml": nil}))
	g.TrustConfigKey = "trust.yaml"
	wantOff(t, g, "the trust Secret holds no trust.yaml")
}

// A subject or prefix on another registry is a store the gate never probed:
// unknown, never on.
func TestGateUnknownForSubjectsOnAnotherRegistry(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Subjects = []string{"oci://zot.acme.internal/acme/", "oci://legacy-harbor.acme/apps/"}
	wantUnknown(t, g, "subjects on legacy-harbor.acme, which is not the registry")
	g.Subjects = []string{"zot.acme.internal/acme/orders@sha256:abc"}
	if on, _, _ := check(t, g); !on {
		t.Fatal("a subject on the registry kept the gate off")
	}
}

func TestGateOffWithoutSubjectsOrPrefix(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Subjects = nil
	wantOff(t, g, "set evidence.registry.subjectPrefixes")
}

func TestGateUnknownBeforeFirstPush(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.LastPush = func() *detect.PushResult { return nil }
	wantUnknown(t, g, "no target snapshot pushed yet to probe zot.acme.internal")
}

func TestGateUnknownAfterFailedPush(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.LastPush = func() *detect.PushResult {
		return &detect.PushResult{Ref: "x", Error: `PUT "https://zot.acme.internal/v2/acme/pacto-targets/manifests/cluster-7f3c": 401`}
	}
	wantUnknown(t, g, "the last target snapshot push failed; see the operator log")
	if _, _, e := check(t, g); strings.Contains(e.Reason, "cluster-7f3c") || strings.Contains(e.Reason, "pacto-targets") {
		t.Fatalf("the row names the snapshot tag: %+v", e)
	}
}

func TestGateUnknownOnProbeError(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Probe = func(context.Context) (capabilities.Capabilities, error) {
		return capabilities.Capabilities{}, errors.New("zot.acme.internal/acme/pacto-targets:cluster-7f3c: not found")
	}
	wantUnknown(t, g, "probing zot.acme.internal failed; see the operator log")
	if _, _, e := check(t, g); strings.Contains(e.Reason, "cluster-7f3c") {
		t.Fatalf("the row names the snapshot tag: %+v", e)
	}
}

func TestGateUnknownOnSecretReadError(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Reader = interceptor.NewClient(fake.NewClientBuilder().WithScheme(newScheme()).Build(), interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "trust", errors.New("no"))
		}})
	wantUnknown(t, g, "reading the trust Secret failed: Forbidden")
	g.Reader = interceptor.NewClient(fake.NewClientBuilder().WithScheme(newScheme()).Build(), interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("dial tcp 10.0.0.1:443: i/o timeout")
		}})
	wantUnknown(t, g, "the API server did not answer")
}

func TestGateReadsTrustSecretByGetOnly(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	g.Reader = interceptor.NewClient(g.Reader.(client.WithWatch), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			t.Fatal("the gate listed")
			return nil
		}})
	if on, _, _ := check(t, g); !on {
		t.Fatal("gate is off")
	}
}

func TestGateRowHoldsNoKeyBytes(t *testing.T) {
	_, _, e := check(t, newGate(trustSecret(keyBytes())))
	for _, s := range []string{e.Value, e.Evidence, e.Reason, e.Hint} {
		if strings.Contains(s, "KEYBYTES") {
			t.Fatalf("entry carries key bytes: %+v", e)
		}
	}
}

func TestGateURLEmptyUnlessOn(t *testing.T) {
	g := newGate(trustSecret(keyBytes()))
	if g.EvidenceURL() != "" || g.Entry().Completeness != detect.Unknown {
		t.Fatalf("before the first check: url %q entry %+v", g.EvidenceURL(), g.Entry())
	}
	check(t, g)
	if g.EvidenceURL() != gateURL || g.Entry().Value != "on" {
		t.Fatalf("on: url %q entry %+v", g.EvidenceURL(), g.Entry())
	}
	// Unknown keeps the remembered state, and the row says why.
	g.LastPush = func() *detect.PushResult { return nil }
	check(t, g)
	if g.EvidenceURL() != gateURL || g.Entry().Value != "unknown" {
		t.Fatalf("unknown: url %q entry %+v", g.EvidenceURL(), g.Entry())
	}
	g.Registry = ""
	check(t, g)
	if g.EvidenceURL() != "" {
		t.Fatalf("off: url %q", g.EvidenceURL())
	}
}

// An operator restart keeps the URL of an Evidence Server that still runs:
// the first check cannot tell before the first push, and the Deployment is
// the current state until a decision is known.
func TestGateRestartKeepsURLOfARunningServer(t *testing.T) {
	running := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: Name}}
	g := newGate(trustSecret(keyBytes()), running)
	g.LastPush = func() *detect.PushResult { return nil }
	check(t, g)
	if g.EvidenceURL() != gateURL {
		t.Fatalf("restart with a running server: url %q", g.EvidenceURL())
	}
	// Without the Deployment it stays empty.
	g = newGate(trustSecret(keyBytes()))
	g.LastPush = func() *detect.PushResult { return nil }
	check(t, g)
	if g.EvidenceURL() != "" {
		t.Fatalf("restart without a server: url %q", g.EvidenceURL())
	}
	// Once a decision is known, an unknown keeps it, whatever runs.
	g = newGate(running)
	check(t, g)
	g.LastPush = func() *detect.PushResult { return nil }
	check(t, g)
	if g.EvidenceURL() != "" {
		t.Fatalf("a known off then unknown: url %q", g.EvidenceURL())
	}
}

func TestPieceExplicitOrGate(t *testing.T) {
	if e := Piece(nil, true); e.Value != "on" || e.Evidence != "evidence.enabled is true" {
		t.Errorf("enabled %+v", e)
	}
	if e := Piece(nil, false); e.Value != "off" || e.Evidence != "evidence.enabled is false" {
		t.Errorf("disabled %+v", e)
	}
	if e := Piece(newGate(), false); e.Value != "unknown" {
		t.Errorf("gate %+v", e)
	}
}
