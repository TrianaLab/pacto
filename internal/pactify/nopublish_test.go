package pactify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// TestInitWritesNoPublishSection: pacto init writes no publish section, no
// signing key and no .pactoignore line for one, at the current
// pactoVersion: pacto ci --publish derives the section.
func TestInitWritesNoPublishSection(t *testing.T) {
	dir := fraudDir(t)
	r, err := Init(context.Background(), Options{Dir: dir, Owner: "risk"}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	text := string(raw)
	if strings.Contains(text, "publish:") || strings.Contains(text, "cosign.key") || !strings.Contains(text, "pactoVersion: \"2.5\"\n") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == ".pactoignore" }) || hasLine(r.Next, "signing key") {
		t.Fatalf("plan: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pactoignore")); err == nil {
		t.Fatal(".pactoignore written")
	}
	if !strings.Contains(text, "# Check it, then see what publishing would do: pacto ci\n") {
		t.Fatalf("the header does not name pacto ci:\n%s", text)
	}
}

// TestInitWritesNoServiceVersion: neither pactify nor blank mode writes
// service.version; the image tag becomes a Found line about the first
// publish.
func TestInitWritesNoServiceVersion(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	blank, err := Init(context.Background(), Options{Dir: filepath.Join(t.TempDir(), "blank"), Owner: "risk", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range []*Result{r, blank} {
		c, err := contract.Parse(strings.NewReader(file(t, res, "pacto.yaml").Content))
		if err != nil || c.Service.Version != "" || c.PactoVersion != contract.Current() {
			t.Fatalf("%s: %+v %v", res.Mode, c, err)
		}
	}
	want := "first publish: 1.0.0 is the image tag in k8s/deployment.yaml; declare service.version to publish it, or pacto ci --publish derives 1.0.0"
	if !slices.Contains(r.Found, want) || hasLine(blank.Found, "first publish") {
		t.Fatalf("found: %v / %v", r.Found, blank.Found)
	}
}

// TestInitDeliveryReadsTheLayout: the Pacto resource and the candidate
// OCIRepository read the repositories publish.Layout names: the bundle at
// <registry>/<name>, or under contracts when the image is there, and the
// deploy artifact at <registry>/deploy/<name>.
func TestInitDeliveryReadsTheLayout(t *testing.T) {
	for _, tc := range []struct{ image, bundle string }{
		{"registry.example/story/images/fraud:1.0.0", "oci://registry.example/story/fraud"},
		{"registry.example/story/fraud:1.0.0", "oci://registry.example/story/contracts/fraud"},
	} {
		dir := fraudDir(t)
		dep := filepath.Join(dir, "k8s", "deployment.yaml")
		raw, _ := os.ReadFile(dep)
		writeTree(t, dir, map[string]string{"k8s/deployment.yaml": strings.Replace(string(raw), "registry.example/story/images/fraud:1.0.0", tc.image, 1)})
		r, err := Init(context.Background(), Options{Dir: dir, Owner: "risk", Delivery: true, Plan: true}, os.WriteFile)
		if err != nil {
			t.Fatal(err)
		}
		if res := file(t, r, "delivery/pacto.yaml").Content; !strings.Contains(res, "oci: "+tc.bundle+":@PACTO_VERSION@\n") {
			t.Errorf("%s: the Pacto resource:\n%s", tc.image, res)
		}
		if test := file(t, r, "delivery/test.yaml").Content; !strings.Contains(test, "  url: oci://registry.example/story/deploy/fraud\n") {
			t.Errorf("%s: delivery/test.yaml:\n%s", tc.image, test)
		}
	}
}

// TestInitDeliveryVerifyPlaceholder: with no publish section the Stages
// verify keyless against an identity that matches nothing, and Unknown says
// who has to name the publisher.
func TestInitDeliveryVerifyPlaceholder(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "  verify:\n    provider: cosign\n    matchOIDCIdentity:\n      - issuer: '^https://token\\.actions\\.githubusercontent\\.com$'\n" +
		"        subject: '^<the identity pacto ci --publish prints>$'\n"
	for _, env := range []string{"test", "prod"} {
		if text := file(t, r, "delivery/"+env+".yaml").Content; !strings.Contains(text, want) || strings.Contains(text, "secretRef") {
			t.Errorf("delivery/%s.yaml:\n%s", env, text)
		}
	}
	if !slices.Contains(r.Unknown, "who publishes fraud: set spec.verify.matchOIDCIdentity in delivery/<env>.yaml to the identity pacto ci --publish prints") ||
		hasLine(r.Next, "cosign-pub") {
		t.Fatalf("unknown: %v next: %v", r.Unknown, r.Next)
	}
}

// TestInitDeliveryShipsHintOnlyWithDeclaredPublish: a derived publish section
// ships delivery/pacto.yaml itself, so only a declared section whose builds
// do not read it gets the hint.
func TestInitDeliveryShipsHintOnlyWithDeclaredPublish(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if hasLine(r.Next, "ship delivery/pacto.yaml") {
		t.Fatalf("a derived section got the hint: %v", r.Next)
	}
	dir := writeTree(t, fraudDir(t), map[string]string{"pacto.yaml": `pactoVersion: "2.5"
service: {name: fraud, version: 1.0.0}
publish:
  bundle: oci://registry.example/story/fraud
  deploy: manifests
  artifacts:
    - {name: manifests, type: manifests, ref: oci://registry.example/story/deploy/fraud, build: {command: [make, manifests]}}
  sign: {key: /keys/cosign.key}
`})
	if r, err = Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if !hasLine(r.Next, "ship delivery/pacto.yaml") || !hasLine(r.Next, "create the Secret cosign-pub") {
		t.Fatalf("a declared section got no hint: %v", r.Next)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	writeTree(t, dir, map[string]string{"pacto.yaml": strings.Replace(string(raw), "[make, manifests]", "[make, manifests, delivery/pacto.yaml]", 1)})
	if r, err = Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile); err != nil || hasLine(r.Next, "ship delivery/pacto.yaml") {
		t.Fatalf("a build that ships it got the hint: %v %v", r.Next, err)
	}
}

// TestInitFromGoldenPathNoTransparencyLog: --no-transparency-log with a
// golden path writes transparencyLog: false under its publish.sign; without
// one it is refused.
func TestInitFromGoldenPathNoTransparencyLog(t *testing.T) {
	_, c := mustFromPath(t, goldenPath(""), Options{NoTransparencyLog: true}, override.Overrides{})
	if s := c.Publish.Sign; s == nil || s.TransparencyLog == nil || *s.TransparencyLog {
		t.Fatalf("sign: %+v", s)
	}
	_, err := Init(context.Background(), Options{Dir: fraudDir(t), NoTransparencyLog: true, Plan: true}, os.WriteFile)
	if err == nil || !strings.Contains(err.Error(), "transparencyLog goes under a golden path's publish.sign") {
		t.Fatalf("got %v", err)
	}
}
