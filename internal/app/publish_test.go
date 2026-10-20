package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// fakeRun stands in for git, docker, cosign, syft and the declared command.
// Every call is recorded as "name arg...".
type fakeRun struct {
	calls  []string
	signed map[string]bool
	fail   string // a call prefix that fails
}

func (f *fakeRun) run(_ context.Context, _ string, env []string, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.HasPrefix(call, f.fail) {
		return nil, errors.New("injected: " + call)
	}
	out := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PACTO_OUT="); ok {
			out = v
		}
	}
	ref := ""
	if len(args) > 0 {
		ref = args[len(args)-1]
	}
	switch {
	case name == "git":
		return []byte("abc123\n"), nil
	case name == "docker":
		p, _ := layout.Write(out, empty.Index)
		img, _ := random.Image(32, 1)
		return nil, p.AppendImage(img)
	case name == "cosign" && args[0] == "public-key":
		return []byte("-----BEGIN PUBLIC KEY-----\n"), nil
	case name == "cosign" && args[0] == "verify":
		if !f.signed[ref] {
			return nil, errors.New("no signatures found")
		}
		return nil, nil
	case name == "cosign" && args[0] == "sign":
		f.signed[ref] = true
		return nil, nil
	case name == "syft":
		return []byte(`{"spdxVersion":"SPDX-2.3"}`), nil
	}
	return nil, os.WriteFile(filepath.Join(out, "deployment.yaml"), []byte("kind: Deployment\n"), 0o644)
}

func publishRegistry(t *testing.T) (string, publish.Registry) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, publish.NewClient(authn.DefaultKeychain, host)
}

func writePublishBundle(t *testing.T, host, extra string) string {
	t.Helper()
	dir := t.TempDir()
	y := `pactoVersion: "2.1"
service: {name: payments, version: 1.0.0}
` + extra + `publish:
  bundle: oci://` + host + `/pacto/payments
  deploy: manifests
  artifacts:
    - {name: api, type: image, ref: "oci://` + host + `/payments", sbom: true, build: {dockerfile: {context: ., file: Dockerfile, platforms: [linux/amd64]}}}
    - {name: manifests, type: manifests, ref: "oci://` + host + `/deploy/payments", build: {command: [./render.sh, --flag]}}
  sign: {key: cosign.key, transparencyLog: false}
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func publishService(f *fakeRun, reg publish.Registry) *Service {
	return &Service{sys: sysDeps{run: f.run, publishRegistry: func() publish.Registry { return reg }}}
}

// TestPublishPlan: --plan lists what is missing and runs nothing but git.
func TestPublishPlan(t *testing.T) {
	host, reg := publishRegistry(t)
	f := &fakeRun{signed: map[string]bool{}}
	res, err := publishService(f, reg).Publish(context.Background(), PublishOptions{Path: writePublishBundle(t, host, ""), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Plan.Ready || res.Plan.Missing != 3 || res.Plan.Revision != "abc123" || len(f.calls) != 1 {
		t.Fatalf("plan: %+v calls %v", res.Plan, f.calls)
	}
}

// TestPublishRunsTheTools: publish builds with docker buildx and the declared
// command, makes an SBOM with syft and signs every digest with cosign, then a
// second run signs nothing.
func TestPublishRunsTheTools(t *testing.T) {
	host, reg := publishRegistry(t)
	f := &fakeRun{signed: map[string]bool{}}
	svc := publishService(f, reg)
	dir := writePublishBundle(t, host, "")
	res, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy == "" || len(f.signed) != 4 {
		t.Fatalf("result %+v signed %v", res, f.signed)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"docker buildx build --output type=oci,dest=",
		"--label org.opencontainers.image.version=1.0.0 --label org.opencontainers.image.revision=r1 --file Dockerfile --platform linux/amd64 .",
		"./render.sh --flag",
		"syft scan registry:" + host + "/payments@",
		"cosign sign --yes --allow-http-registry --tlog-upload=false --use-signing-config=false --key " + filepath.Join(dir, "cosign.key"),
		"cosign verify --allow-http-registry --insecure-ignore-tlog --key ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no call with %q in:\n%s", want, joined)
		}
	}
	f.calls = nil
	again, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || len(again.Actions) != 0 || slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "cosign sign") }) {
		t.Fatalf("second run: %v %v %v", err, again.Actions, f.calls)
	}
}

func TestPublishRefusals(t *testing.T) {
	host, reg := publishRegistry(t)
	ctx := context.Background()
	f := &fakeRun{signed: map[string]bool{}}
	if _, err := publishService(f, reg).Publish(ctx, PublishOptions{Path: writeInvalidBundle(t)}); err == nil {
		t.Fatal("an invalid contract is refused")
	}
	local := "dependencies: [{name: orders, ref: ../orders, required: true, compatibility: ^1.0.0}]\n"
	res, err := publishService(f, reg).Publish(ctx, PublishOptions{Path: writePublishBundle(t, host, local), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Ready || !slices.ContainsFunc(res.Plan.Checks, func(c publish.Check) bool { return c.Name == "references" && !c.Passed }) {
		t.Fatalf("a local reference is a failed check: %+v", res.Plan.Checks)
	}
	nogit := &fakeRun{signed: map[string]bool{}, fail: "git"}
	res, _ = publishService(nogit, reg).Publish(ctx, PublishOptions{Path: writePublishBundle(t, host, ""), Plan: true})
	if res.Plan.Revision != "" || res.Plan.Ready {
		t.Fatalf("no git revision: %+v", res.Plan)
	}
}

func TestExecTools(t *testing.T) {
	ctx := context.Background()
	f := &fakeRun{signed: map[string]bool{}}
	tools := execTools{dir: t.TempDir(), run: f.run}
	off := false
	cases := []struct {
		sign contract.PublishSign
		want string
	}{
		{contract.PublishSign{KMS: "awskms:///a"}, "cosign sign --yes --key awskms:///a ghcr.io/a@sha256:1"},
		{contract.PublishSign{Keyless: true}, "cosign verify --certificate-identity-regexp .* --certificate-oidc-issuer-regexp .* ghcr.io/a@sha256:1"},
		{contract.PublishSign{Key: "/abs/k", TransparencyLog: &off}, "cosign sign --yes --tlog-upload=false --use-signing-config=false --key /abs/k ghcr.io/a@sha256:1"},
	}
	for _, c := range cases {
		f.calls, f.signed = nil, map[string]bool{}
		if signed, err := tools.Sign(ctx, c.sign, "ghcr.io/a@sha256:1"); err != nil || !signed {
			t.Fatalf("%+v: %v %v", c.sign, signed, err)
		}
		if !slices.Contains(f.calls, c.want) {
			t.Errorf("%+v: no %q in %v", c.sign, c.want, f.calls)
		}
	}
	if signed, err := tools.Sign(ctx, contract.PublishSign{Key: "/abs/k", TransparencyLog: &off}, "ghcr.io/a@sha256:1"); err != nil || signed {
		t.Fatalf("an existing signature is kept: %v %v", signed, err)
	}
	for _, fail := range []string{"cosign sign", "cosign public-key"} {
		bad := execTools{dir: t.TempDir(), run: (&fakeRun{signed: map[string]bool{}, fail: fail}).run}
		if _, err := bad.Sign(ctx, contract.PublishSign{Key: "k"}, "ghcr.io/a@sha256:1"); err == nil {
			t.Errorf("%s failing must surface", fail)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := tools.Sign(ctx, contract.PublishSign{Key: "k"}, "ghcr.io/a@sha256:1"); err == nil {
		t.Fatal("no temporary file for the public key")
	}
	t.Setenv("PACTO_INSECURE_REGISTRIES", "zot.local:5000")
	for ref, want := range map[string]bool{"localhost:5000/a": true, "127.0.0.1/a": true, "zot.local:5000/a": true, "ghcr.io/a": false} {
		if insecureHost(ref) != want {
			t.Errorf("insecureHost(%s) != %v", ref, want)
		}
	}
	if _, err := tools.SBOM(ctx, "ghcr.io/a@sha256:1"); err != nil {
		t.Fatal(err)
	}
}

func TestExecRun(t *testing.T) {
	out, err := execRun(context.Background(), t.TempDir(), []string{"X=1"}, "sh", "-c", "echo $X")
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("%q %v", out, err)
	}
	_, err = execRun(context.Background(), t.TempDir(), nil, "sh", "-c", "echo nope >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("the error carries stderr: %v", err)
	}
	if long := tail(strings.Repeat("x", 3000)); len(long) > 2010 || !strings.HasPrefix(long, "…") {
		t.Fatalf("tail: %d", len(long))
	}
	if defaultPublishRegistry() == nil {
		t.Fatal("the default registry")
	}
}
