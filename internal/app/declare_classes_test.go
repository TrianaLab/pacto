package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// withClassPolicy points the installation's publisher trust config at a
// delivery policy signed by a policy key and holding classes, pushed to the
// env's registry; a policy signed by another key when forged.
func (e *declareEnv) withClassPolicy(t *testing.T, classes []delivery.SignerClassRule, forged bool) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	if forged {
		_, key, _ = ed25519.GenerateKey(rand.Reader)
	}
	payload, _ := json.Marshal(delivery.PolicyStatement{Type: delivery.StatementType, Subject: []delivery.Subject{},
		PredicateType: delivery.PredicatePublisherPolicy, Predicate: delivery.PublisherPolicy{Signers: classes, At: "2026-11-18T00:00:00Z"}})
	bundle, err := edSigner(key).Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := mutate.AppendLayers(empty.Image, static.NewLayer(bundle, types.MediaType(delivery.BundleMediaType)))
	c := e.reg.(*publish.Client)
	d, err := c.Write(t.Context(), e.host+"/policy", mutate.MediaType(img, types.OCIManifestSchema1))
	if err == nil {
		err = c.Tag(t.Context(), e.host+"/policy", d, "v1")
	}
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(delivery.PublisherConfig{Registry: "ghcr.io/acme", Policy: e.host + "/policy:v1", PolicySigners: policySigners(t, pub)})
	cm := identities()
	_ = unstructured.SetNestedField(cm.Object, string(cfg), "data", delivery.PublishersKey)
	if err := e.dyn.Tracker().Update(gvrConfigMaps, cm, delivery.PolicyNamespace); err != nil {
		t.Fatal(err)
	}
}

// The verified installation policy's signer rules classify the person who
// logs in: one it names an agent writes a draft, never a declared version.
func TestDeclarePolicyClassRuleMakesALoginAnAgent(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withClassPolicy(t, []delivery.SignerClassRule{{Name: "bots", Issuer: personIssuer, Subject: `^ana@acme\.com$`, Class: delivery.ClassAgent}}, false)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req())
	if !res.Draft || res.Plan.State == "declared" {
		t.Fatalf("an agent by policy wrote a declared version: %+v", res)
	}
}

// A policy that does not verify classifies by the default rule, and the plan
// says so.
func TestDeclarePolicyUnverifiedUsesDefaultRule(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withClassPolicy(t, []delivery.SignerClassRule{{Name: "bots", Issuer: personIssuer, Subject: `^ana@acme\.com$`, Class: delivery.ClassAgent}}, true)
	p, _ := e.plan(t, e.req())
	if !strings.Contains(strings.Join(p.Notes, " "), "could not be verified") {
		t.Fatalf("%+v", p.Notes)
	}
}
