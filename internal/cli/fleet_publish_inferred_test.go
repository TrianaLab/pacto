package cli_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// inferredRegistry is an in-memory registry with the Referrers API, and a
// seed key file.
func inferredRegistry(t *testing.T) (string, string) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := filepath.Join(t.TempDir(), "installation.key")
	mustWrite(t, key, base64.StdEncoding.EncodeToString(priv.Seed()))
	return strings.TrimPrefix(srv.URL, "http://"), key
}

func TestFleetPublishInferredPushes(t *testing.T) {
	host, key := inferredRegistry(t)
	out, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", "oci://"+host+"/demo", "--key", key, "--local", writeExternalFixture(t))
	if err != nil {
		t.Fatalf("publish-inferred: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Published "+host+"/demo/pacto-inferred/stripe:0.0.1") || !strings.Contains(out, "2 published, 0 unchanged, 0 skipped") {
		t.Errorf("output:\n%s", out)
	}
	reg := publish.NewClient(authn.NewMultiKeychain(), host)
	h, err := reg.Head(context.Background(), host+"/demo/pacto-inferred/stripe:0.0.1")
	if err != nil || h == nil || h.Annotations[contract.AnnotationProvenance] != contract.ProvenanceInferred {
		t.Fatalf("head = %+v, %v", h, err)
	}
	// The published bundle validates end to end: a folder rebuilds its record,
	// a bundle's surface record is checked as pushed.
	if out, _, err := execFleetStore(t, oci.NewClient(authn.NewMultiKeychain()), "validate", "oci://"+host+"/demo/pacto-inferred/stripe:0.0.1"); err != nil {
		t.Fatalf("validate the inferred bundle: %v\n%s", err, out)
	}
	out, _, err = execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", writeExternalFixture(t))
	if err != nil || !strings.Contains(out, "0 published, 2 unchanged") {
		t.Errorf("second run: %v\n%s", err, out)
	}
}

func TestFleetPublishInferredJSONOneDocument(t *testing.T) {
	host, key := inferredRegistry(t)
	out, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", writeExternalFixture(t), "--output-format", "json")
	var rep inferredcontract.Report
	dec := json.NewDecoder(strings.NewReader(out))
	if err != nil || dec.Decode(&rep) != nil || len(rep.Published) != 2 || dec.More() {
		t.Fatalf("json: %v\n%s", err, out)
	}
}

func TestFleetPublishInferredReportsErrors(t *testing.T) {
	_, key := inferredRegistry(t)
	fixture := writeExternalFixture(t)
	// Another installation that rebuilds a tag byte for byte may sign it: it
	// would have pushed the same bytes. A registry that cannot be read fails.
	out, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", "127.0.0.1:1/demo", "--key", key, "--local", fixture)
	if err == nil || !strings.Contains(out, "Error 127.0.0.1:1/demo/pacto-inferred/stripe ") || !strings.Contains(out, "0 published") {
		t.Errorf("text: %v\n%s", err, out)
	}
	out, _, err = execFleet(t, "fleet", "publish-inferred", "--registry", "127.0.0.1:1/demo", "--key", key, "--local", fixture, "--output-format", "json")
	var doc struct {
		Errors []inferredcontract.Entry `json:"errors"`
		Exit   struct{ Code int }       `json:"exit"`
	}
	if err == nil || json.Unmarshal([]byte(out), &doc) != nil || doc.Exit.Code != 1 || len(doc.Errors) != 2 {
		t.Errorf("json: %v\n%s", err, out)
	}
}

func TestFleetPublishInferredRefusesWithoutKey(t *testing.T) {
	host, _ := inferredRegistry(t)
	if _, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo"); err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("no --key = %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.key")
	mustWrite(t, bad, "not a key")
	if _, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", bad, "--local", writeExternalFixture(t)); err == nil {
		t.Error("an unreadable key was accepted")
	}
	key := filepath.Join(t.TempDir(), "k")
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mustWrite(t, key, base64.StdEncoding.EncodeToString(priv.Seed()))
	if _, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", writeExternalFixture(t), "--external-catalog", "oci://"+host+"/catalog:latest"); err == nil {
		t.Error("a fleet that cannot be built published")
	}
}

func TestFleetPublishInferredRepairsAndCaps(t *testing.T) {
	host, key := inferredRegistry(t)
	root := t.TempDir()
	deps := ""
	for i := range inferredcontract.MaxInferredPublishesPerRun + 1 {
		deps += "  - name: vendor-" + string(rune('a'+i)) + "\n    external: true\n"
	}
	mustWrite(t, filepath.Join(root, "checkout", "pacto.yaml"), "pactoVersion: \"2.0\"\nservice:\n  name: checkout\n  version: \"1.0.0\"\ndependencies:\n"+deps)
	out, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", root)
	if err != nil || !strings.Contains(out, "20 published") || !strings.Contains(out, "per-run bound") {
		t.Fatalf("capped: %v\n%s", err, out)
	}
	// Another installation's key signs a tag it rebuilds byte for byte.
	_, other := inferredRegistry(t)
	out, _, err = execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", other, "--local", root)
	if err != nil || !strings.Contains(out, "Signed "+host+"/demo/pacto-inferred/vendor-a:0.0.1, pushed earlier without its signature") {
		t.Errorf("repair: %v\n%s", err, out)
	}
}

// TestPublishInferredPrintsNextStep: each published contract names the
// question to ask about it next.
func TestPublishInferredPrintsNextStep(t *testing.T) {
	host, key := inferredRegistry(t)
	out, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", writeExternalFixture(t))
	if err != nil || !strings.Contains(out, "pacto-inferred/stripe:0.0.1 (sha256:") || !strings.Contains(out, ")\n  next: pacto impact --changed external:stripe\n") {
		t.Errorf("publish-inferred: %v\n%s", err, out)
	}
}
