package pactify

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const pathDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// pathContract is a golden path that carries every value creation resets.
const pathContract = `pactoVersion: "2.4"
service:
  name: http-service
  version: 1.0.0
  owner:
    team: platform
  domain: platform
  summary: An HTTP API on the platform runtime
  offering: http-api
  lifecycle:
    deprecated: "2026-01-01"
    successor: oci://ghcr.io/platform/http-service-v2
  template:
    plugins:
      - name: dockerfile
        description: A Dockerfile for a Go HTTP service
interfaces:
  - name: api
    type: openapi
    ref: interfaces/openapi.yaml
configurations:
  - name: default
    schema: configuration/schema.json
    required: true
    values:
      size: 1
metadata:
  repo: https://github.com/acme/paths
  summary: The HTTP golden path
readiness:
  minScore: 80
  expires: 2099-12-31
  claims:
    - id: runbook
      type: document
      category: documentation
      status: done
      evidence: docs/runbook.md
      weight: 100
publish:
  bundle: oci://ghcr.io/platform/http-service
  artifacts:
    - name: api
      type: image
      ref: oci://ghcr.io/platform/images/http-service
      build:
        dockerfile:
          context: .
    - name: base
      type: image
      ref: oci://ghcr.io/platform/images/base
      build:
        dockerfile:
          context: base
  sign:
    key: cosign.key
`

const pathSchema = `{"type": "object", "properties": {"size": {"type": "integer"}}, "additionalProperties": false}`

const pathOpenAPI = "openapi: \"3.0.0\"\ninfo:\n  title: http\n  version: 1.0.0\npaths: {}\n"

// goldenPath is a path FS with contract replacing its pacto.yaml when given.
func goldenPath(contractText string) fstest.MapFS {
	if contractText == "" {
		contractText = pathContract
	}
	return fstest.MapFS{
		"pacto.yaml":                {Data: []byte(contractText)},
		"interfaces/openapi.yaml":   {Data: []byte(pathOpenAPI)},
		"configuration/schema.json": {Data: []byte(pathSchema)},
		"pacto.lock":                {Data: []byte("lockVersion: 1\n")},
		".pactoignore":              {Data: []byte("cosign.key\n")},
		"docs/runbook.md":           {Data: []byte("# Runbook\n")},
	}
}

// fromPath plans a service created from fsys in a new folder named gift-cards.
func fromPath(t *testing.T, fsys fs.FS, opts Options, ov override.Overrides) (*Result, *contract.Contract, error) {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = filepath.Join(t.TempDir(), "gift-cards")
	}
	if opts.Owner == "" {
		opts.Owner = "checkout"
	}
	opts.Plan = true
	if opts.Template == nil {
		opts.Template = &Template{}
	}
	opts.Template.FS, opts.Template.Ref, opts.Template.Digest = fsys, "oci://ghcr.io/platform/http-service:1.0.0", pathDigest
	opts.Template.Overrides = ov
	if opts.Template.Find == nil {
		opts.Template.Find = notInstalled
	}
	r, err := Init(context.Background(), opts, nil)
	if err != nil {
		return r, nil, err
	}
	c, perr := contract.Parse(strings.NewReader(file(t, r, "pacto.yaml").Content))
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	return r, c, nil
}

func mustFromPath(t *testing.T, fsys fs.FS, opts Options, ov override.Overrides) (*Result, *contract.Contract) {
	t.Helper()
	r, c, err := fromPath(t, fsys, opts, ov)
	if err != nil {
		t.Fatal(err)
	}
	return r, c
}

// wantLines fails for each want no line in lines contains.
func wantLines(t *testing.T, kind string, lines []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !hasLine(lines, w) {
			t.Errorf("no %s line %q: %v", kind, w, lines)
		}
	}
}

func TestTemplateResetsIdentity(t *testing.T) {
	r, c := mustFromPath(t, goldenPath(""), Options{Domain: "commerce"}, override.Overrides{})
	s := c.Service
	if r.Mode != modeTemplate || c.PactoVersion != "2.4" || s.Name != "gift-cards" || s.Version != "0.1.0" || s.Owner.Team != "checkout" || s.Domain != "commerce" {
		t.Fatalf("identity: %+v", c)
	}
	if s.Origin == nil || *s.Origin != (contract.Origin{Ref: "oci://ghcr.io/platform/http-service:1.0.0", Digest: pathDigest}) {
		t.Fatalf("origin: %+v", s.Origin)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.HasPrefix(text, "pactoVersion: \"2.4\"\nservice:\n  name: gift-cards\n  version: 0.1.0\n  owner:\n") {
		t.Errorf("order:\n%s", text)
	}
}

func TestTemplateSaysWhatDidNotCarryOver(t *testing.T) {
	r, c := mustFromPath(t, goldenPath(""), Options{Domain: "commerce"}, override.Overrides{})
	if s := c.Service; s.Template != nil || s.Summary != "" || s.Lifecycle != nil || c.Readiness != nil || s.Offering != "" || c.Metadata != nil {
		t.Fatalf("carried over: %+v", c)
	}
	wantLines(t, "Unknown", r.Unknown, "service.summary: the golden path's summary", "service.lifecycle: the golden path's lifecycle (deprecated 2026-01-01",
		"service.offering: the golden path's offering http-api", "metadata.repo", "metadata.summary", "readiness: the golden path's readiness evidence")
	wantLines(t, "Found", r.Found, "golden path http-service 1.0.0 from oci://ghcr.io/platform/http-service:1.0.0")
	wantLines(t, "Next", r.Next, "deprecated since 2026-01-01; successor oci://ghcr.io/platform/http-service-v2")
	if hasLine(r.Unknown, "service.domain") {
		t.Errorf("unknown: %v", r.Unknown)
	}

	r, c = mustFromPath(t, goldenPath(""), Options{Name: "named", Owner: "team/checkout"}, override.Overrides{})
	if c.Service.Name != "named" || c.Service.Domain != "" {
		t.Errorf("no --domain: %+v", c.Service)
	}
	wantLines(t, "Next", r.Next, "owner.team is the team's bare name")
	wantLines(t, "Unknown", r.Unknown, "service.domain: the golden path's domain platform was not carried over; pass --domain")
}

func TestTemplateOverridesOrder(t *testing.T) {
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte("configurations:\n  - name: default\n    schema: configuration/schema.json\n    required: true\n    values:\n      size: 5\nservice:\n  summary: null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, c := mustFromPath(t, goldenPath(""), Options{Name: "gift-cards"}, override.Overrides{
		ValueFiles: []string{values},
		Values:     []map[string]any{{"metadata": map[string]any{"team-chat": "#checkout"}}},
		SetValues:  []string{"service.version=1.0.0", "service.name=x", "service.template.plugins[0].name=x", "service.offering=postgresql"},
	})
	if c.Service.Version != "1.0.0" || c.Service.Name != "gift-cards" || c.Service.Template != nil || c.Service.Offering != "postgresql" || c.Service.Summary != "" {
		t.Fatalf("service: %+v", c.Service)
	}
	if c.Configurations[0].Values["size"] != 5 || c.Metadata["team-chat"] != "#checkout" || hasLine(r.Unknown, "service.offering") {
		t.Fatalf("values: %+v %+v %v", c.Configurations, c.Metadata, r.Unknown)
	}
}

func TestTemplateOverrideErrors(t *testing.T) {
	for set, want := range map[string]string{
		"bad":          "applying the values to the golden path",
		"service=x":    "leave no service object",
		"interfaces=x": "is not valid: ",
	} {
		if _, _, err := fromPath(t, goldenPath(""), Options{}, override.Overrides{SetValues: []string{set}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--set %s: %v", set, err)
		}
	}
}

func TestTemplatePublishRewrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   Options
		bundle string
	}{
		{"describe-only", Options{DescribeOnly: true}, ""},
		{"registry", Options{Registry: "oci://ghcr.io/acme/", NoTransparencyLog: true}, "oci://ghcr.io/acme/gift-cards"},
		{"placeholder", Options{}, "oci://registry.example/gift-cards"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, c := mustFromPath(t, goldenPath(""), tc.opts, override.Overrides{})
			if tc.bundle == "" && c.Publish != nil || tc.bundle != "" && c.Publish.Bundle != tc.bundle {
				t.Fatalf("publish: %+v", c.Publish)
			}
		})
	}

	// A path with no publish section and the path's own name keep what they had.
	m := map[string]any{"service": map[string]any{}}
	if rewritePublish(m, "a", "b", "", false) != nil || len(m) != 1 {
		t.Errorf("no publish: %v", m)
	}
	m = map[string]any{"publish": map[string]any{"artifacts": []any{map[string]any{"ref": "oci://r/a"}, "odd"}}}
	if rewritePublish(m, "a", "a", "r", false) != nil {
		t.Errorf("same name: %v", m)
	}
}

func TestTemplatePublishLines(t *testing.T) {
	r, _ := mustFromPath(t, goldenPath(""), Options{DescribeOnly: true}, override.Overrides{})
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == ".pactoignore" }) || hasLine(r.Next, "pacto publish") {
		t.Errorf("describe-only: %+v", r)
	}

	r, c := mustFromPath(t, goldenPath(""), Options{Registry: "ghcr.io/acme", NoTransparencyLog: true}, override.Overrides{})
	if hasLine(r.Unknown, "registry") || *c.Publish.Sign.TransparencyLog || hasLine(r.Next, "transparencyLog") {
		t.Errorf("registry: %+v %+v", r, c.Publish.Sign)
	}

	r, c = mustFromPath(t, goldenPath(""), Options{}, override.Overrides{})
	wantLines(t, "Unknown", r.Unknown, "registry: no --registry was given")
	wantLines(t, "Next", r.Next, "put the signing key at cosign.key", "run pacto publish --plan to see what publishing gift-cards builds")
	wantLines(t, "Found", r.Found, "publish.artifacts api: oci://ghcr.io/platform/images/http-service becomes oci://registry.example/gift-cards",
		"publish.artifacts base: oci://ghcr.io/platform/images/base becomes oci://registry.example/base")
	if f := file(t, r, ".pactoignore"); f.Action != Add || f.Content != signKey+"\n" {
		t.Errorf(".pactoignore: %+v", f)
	}
	if arts := c.Publish.Artifacts; arts[0].Ref != "oci://registry.example/gift-cards" || arts[1].Ref != "oci://registry.example/base" {
		t.Errorf("artifacts: %+v", arts)
	}
}

func TestTemplateCopiesFilesAddOnly(t *testing.T) {
	r, _ := mustFromPath(t, goldenPath(""), Options{}, override.Overrides{})
	for _, f := range []string{"interfaces/openapi.yaml", "configuration/schema.json", "docs/runbook.md"} {
		if got := file(t, r, f); got.Action != Add || got.Content == "" {
			t.Errorf("%s: %+v", f, got)
		}
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == "pacto.lock" }) || !hasLine(r.Next, "run pacto lock") {
		t.Errorf("lock: %+v", r)
	}
	if file(t, r, ".pactoignore").Content != signKey+"\n" {
		t.Errorf("the path's .pactoignore was copied: %+v", r.Files)
	}
}

func TestTemplateNeverOverwrites(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{"docs/runbook.md": "mine\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Owner: "checkout", Template: &Template{FS: goldenPath(""), Ref: "../paths", Digest: pathDigest}},
		func(p string, data []byte, mode os.FileMode) error { return os.WriteFile(p, data, mode) })
	if err != nil {
		t.Fatal(err)
	}
	if got := file(t, r, "docs/runbook.md"); got.Action != Keep || got.Content != "" {
		t.Errorf("runbook: %+v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "docs/runbook.md")); string(data) != "mine\n" {
		t.Errorf("runbook overwritten: %q", data)
	}
	if !r.Applied || r.Steps[1].Status != fleet.StepStatusDone {
		t.Errorf("applied: %+v", r.Steps)
	}
	if _, err := Init(context.Background(), Options{Dir: dir, Owner: "checkout", Template: &Template{FS: goldenPath("")}}, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing pacto.yaml: %v", err)
	}
}

func TestTemplateRefusals(t *testing.T) {
	plain := strings.Replace(pathContract, "  template:\n    plugins:\n      - name: dockerfile\n        description: A Dockerfile for a Go HTTP service\n", "", 1)
	for _, tc := range []struct {
		name string
		fsys fs.FS
		opts Options
		want string
	}{
		{"not a template", goldenPath(plain), Options{}, "is not a golden path"},
		{"no contract", fstest.MapFS{}, Options{}, "reading the golden path"},
		{"unparsable", goldenPath("service: ["), Options{}, "reading the golden path"},
		{"delivery describe-only", goldenPath(""), Options{Delivery: true, DescribeOnly: true}, "drop one of the two"},
		{"fleet", goldenPath(""), Options{FleetRoots: []string{"x"}}, "drop --fleet"},
		{"cluster", goldenPath(""), Options{Inferred: []Inferred{{Name: "x"}}}, "drop --cluster"},
		{"unreadable file", errFS{goldenPath("")}, Options{}, "reading docs/runbook.md from the golden path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := fromPath(t, tc.fsys, tc.opts, override.Overrides{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	_, err := Init(context.Background(), Options{Dir: t.TempDir(), Plan: true, Template: &Template{FS: goldenPath("")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "pass --owner") {
		t.Errorf("missing owner: %v", err)
	}
}

// errFS fails to read docs/runbook.md.
type errFS struct{ fstest.MapFS }

func (e errFS) ReadFile(name string) ([]byte, error) {
	if name == "docs/runbook.md" {
		return nil, errors.New("boom")
	}
	return e.MapFS.ReadFile(name)
}

func (e errFS) Open(name string) (fs.File, error) {
	if name == "docs/runbook.md" {
		return nil, errors.New("boom")
	}
	return e.MapFS.Open(name)
}

func TestTemplateStrictValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gift-cards")
	validate := func(context.Context, *contract.Contract, []byte, fs.FS) []validation.Stage {
		return []validation.Stage{{Name: "configuration-refs", Status: validation.StageFail,
			Errors: []contract.ValidationError{{Code: "CONFIG_REF_UNRESOLVED", Path: "configurations[1].ref", Message: "cannot resolve"}}}}
	}
	_, err := Init(context.Background(), Options{Dir: dir, Owner: "checkout", Template: &Template{FS: goldenPath(""), Ref: "oci://p", Validate: validate}},
		func(string, []byte, os.FileMode) error { t.Fatal("wrote a file"); return nil })
	if err == nil || !strings.Contains(err.Error(), "the contract created from oci://p is not valid:\nCONFIG_REF_UNRESOLVED configurations[1].ref: cannot resolve") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(dir); serr == nil {
		t.Error("the folder was created")
	}
}

func TestTemplateValidatesKeptFiles(t *testing.T) {
	invalid := "openapi: [\n"
	dir := writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{"interfaces/openapi.yaml": invalid})
	var seen string
	validate := func(_ context.Context, _ *contract.Contract, _ []byte, fsys fs.FS) []validation.Stage {
		data, _ := fs.ReadFile(fsys, "interfaces/openapi.yaml")
		seen = string(data)
		return nil
	}
	r, _ := mustFromPath(t, goldenPath(""), Options{Dir: dir, Template: &Template{Validate: validate}}, override.Overrides{})
	if seen != invalid || file(t, r, "interfaces/openapi.yaml").Action != Keep {
		t.Errorf("validator saw %q", seen)
	}
	if _, _, err := fromPath(t, goldenPath(""), Options{Dir: dir}, override.Overrides{}); err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("real validator: %v", err)
	}
	// A folder that holds a directory where the path has a file cannot be read.
	dir = writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{"docs/runbook.md/x": ""})
	if _, _, err := fromPath(t, goldenPath(""), Options{Dir: dir}, override.Overrides{}); err == nil || !strings.Contains(err.Error(), "reading docs/runbook.md, which the folder already holds") {
		t.Errorf("unreadable kept file: %v", err)
	}
}

func TestCreationStepsAtCreation(t *testing.T) {
	refPath := strings.Replace(pathContract, "configurations:\n", "configurations:\n  - name: db\n    ref: oci://ghcr.io/platform/postgresql:1.0.0\n", 1) +
		"dependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/platform/postgresql:1.0.0\n    required: true\n    compatibility: ^1.0.0\n"
	pass := func(context.Context, *contract.Contract, []byte, fs.FS) []validation.Stage {
		return []validation.Stage{{Name: "structural", Status: validation.StagePass}}
	}
	signed := fleet.CreationStep{Status: fleet.StepStatusDone, Detail: "signed by platform"}
	r, _ := mustFromPath(t, goldenPath(refPath), Options{Template: &Template{Validate: pass, Signature: signed}}, override.Overrides{})
	want := map[string]string{fleet.StepSignature: fleet.StepStatusDone, fleet.StepContract: fleet.StepStatusPlanned, fleet.StepValidation: fleet.StepStatusDone,
		fleet.StepPublished: fleet.StepStatusUnknown, fleet.StepFleet: fleet.StepStatusUnknown, fleet.StepDeployed: fleet.StepStatusUnknown, fleet.StepProvisioning: fleet.StepStatusUnknown, fleet.StepPlugin: fleet.StepStatusSkipped}
	if len(r.Steps) != len(want) {
		t.Fatalf("steps: %+v", r.Steps)
	}
	for _, s := range r.Steps {
		if want[s.Step] != s.Status {
			t.Errorf("%s = %s, want %s", s.Step, s.Status, want[s.Step])
		}
	}
	if r.Steps[0].Detail != "signed by platform" || r.Steps[2].Detail != "strict, references resolved" ||
		r.Steps[3].Name != "dockerfile" || !strings.Contains(r.Steps[7].Detail, "configuration db (oci://ghcr.io/platform/postgresql:1.0.0), dependency postgresql") {
		t.Errorf("details: %+v", r.Steps)
	}

	// No signature step and the local validator: unknown, never done.
	r, _ = mustFromPath(t, goldenPath(""), Options{}, override.Overrides{})
	if r.Steps[0].Status != fleet.StepStatusUnknown || r.Steps[7].Status != fleet.StepStatusNotApplicable {
		t.Errorf("steps: %+v", r.Steps)
	}

	notEnforced := func(context.Context, *contract.Contract, []byte, fs.FS) []validation.Stage {
		return []validation.Stage{{Name: "configuration-refs", Status: validation.StagePass, Warnings: []contract.ValidationWarning{
			{Code: "CONFIG_REF_NOT_ENFORCED"}, {Code: "POLICY_REF_UNVERIFIED"}, {Code: "CONFIG_REF_NOT_ENFORCED"}, {Code: "OTHER"}}}}
	}
	r, _ = mustFromPath(t, goldenPath(""), Options{Template: &Template{Validate: notEnforced}}, override.Overrides{})
	if v := r.Steps[2]; v.Status != fleet.StepStatusUnknown || !strings.HasSuffix(v.Detail, ": CONFIG_REF_NOT_ENFORCED, POLICY_REF_UNVERIFIED") {
		t.Errorf("validation: %+v", v)
	}
}

// deployPath is the golden path publishing manifests for Flux under
// publish.deploy.
var deployPath = strings.Replace(pathContract, "  sign:\n", "    - name: manifests\n      type: manifests\n      ref: oci://ghcr.io/platform/deploy/http-service\n"+
	"      build:\n        command: [make, manifests]\n  deploy: manifests\n  sign:\n", 1)

func deliveryStepOf(t *testing.T, r *Result) fleet.CreationStep {
	t.Helper()
	i := slices.IndexFunc(r.Steps, func(s fleet.CreationStep) bool { return s.Step == fleet.StepDelivery })
	if i < 0 || r.Steps[i+1].Step != fleet.StepPublished {
		t.Fatalf("no delivery step before published: %+v", r.Steps)
	}
	return r.Steps[i]
}

func TestTemplateDelivery(t *testing.T) {
	// No publish.deploy: nothing planned, and the step says why.
	r, _ := mustFromPath(t, goldenPath(""), Options{Delivery: true}, override.Overrides{})
	if s := deliveryStepOf(t, r); s.Status != fleet.StepStatusUnknown || !strings.Contains(s.Detail, "publish.deploy") {
		t.Errorf("no deploy: %+v", s)
	}
	wantLines(t, "unknown", r.Unknown, "publishes no manifests for Flux to apply")

	// publish.deploy in a new folder: the Pacto resource only, no Stage.
	r, _ = mustFromPath(t, goldenPath(deployPath), Options{Delivery: true}, override.Overrides{})
	if s := deliveryStepOf(t, r); s.Status != fleet.StepStatusUnknown || !strings.Contains(s.Detail, "the Pacto resource only: delivery/pacto.yaml; no Stage") {
		t.Errorf("no tag: %+v", s)
	}
	if !slices.ContainsFunc(r.Files, func(f File) bool {
		return f.Path == "delivery/pacto.yaml" && strings.Contains(f.Content, "oci://registry.example/gift-cards")
	}) {
		t.Errorf("files: %+v", r.Files)
	}

	// A folder whose workload runs a version: every Stage is planned, a
	// Stage already there is kept and left unchecked, so applying leaves the
	// step unknown rather than done.
	dir, opts := taggedFolder(t, "delivery/qa.yaml")
	r, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if s := deliveryStepOf(t, r); s.Status != fleet.StepStatusUnknown || s.Detail != "the Pacto resource and Stages: delivery/pacto.yaml, delivery/prod.yaml; "+
		"delivery files already exist and were kept; Pacto did not write or check them: delivery/qa.yaml" {
		t.Errorf("stages: %+v", s)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "delivery/qa.yaml")); string(b) != "mine\n" {
		t.Errorf("overwrote delivery/qa.yaml: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "delivery/prod.yaml")); !strings.Contains(string(b), "name: creds") {
		t.Errorf("prod stage: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".pactoignore")); !strings.Contains(string(b), "delivery/") {
		t.Errorf(".pactoignore: %s", b)
	}

}

// taggedFolder is a folder whose workload runs a version, holding the
// delivery files named, and the --delivery options for qa and prod.
func taggedFolder(t *testing.T, delivery ...string) (string, Options) {
	t.Helper()
	files := map[string]string{"k8s/app.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: gift-cards}\nspec:\n  template:\n    spec:\n      containers: [{name: api, image: ghcr.io/acme/gift-cards:0.1.0}]\n"}
	for _, f := range delivery {
		files[f] = "mine\n"
	}
	dir := writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), files)
	return dir, Options{Dir: dir, Owner: "checkout", Delivery: true, Environments: []string{"qa", "prod"}, RegistrySecret: "creds",
		Template: &Template{FS: goldenPath(deployPath), Ref: "oci://ghcr.io/platform/http-service:1.0.0", Digest: pathDigest, Find: notInstalled}}
}

// TestTemplateDeliveryKept: the delivery step is done only for files this
// run wrote. Delivery files already there are kept and never checked, so
// the step stays unknown and says so.
func TestTemplateDeliveryKept(t *testing.T) {
	_, opts := taggedFolder(t, "delivery/pacto.yaml", "delivery/qa.yaml", "delivery/prod.yaml")
	r, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if s := deliveryStepOf(t, r); s.Status != fleet.StepStatusUnknown || s.Detail != "delivery files already exist and were kept; "+
		"Pacto did not write or check them: delivery/pacto.yaml, delivery/qa.yaml, delivery/prod.yaml" {
		t.Errorf("all kept: %+v", s)
	}

	_, opts = taggedFolder(t)
	if r, err = Init(context.Background(), opts, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if s := deliveryStepOf(t, r); s.Status != fleet.StepStatusDone || s.Detail != "the Pacto resource and Stages: delivery/pacto.yaml, delivery/qa.yaml, delivery/prod.yaml" {
		t.Errorf("all added: %+v", s)
	}
}

func TestTemplateHelpers(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": nil, "c": []any{map[string]any{"d": nil, "e": 1}}}}
	pruneNulls(m)
	if _, ok := m["a"].(map[string]any)["b"]; ok || len(m["a"].(map[string]any)["c"].([]any)[0].(map[string]any)) != 1 {
		t.Errorf("pruneNulls: %v", m)
	}
	deletePath(m, "top")
	deletePath(m, "x.y")
	if pathOf(m, "a.c") == nil || pathOf(m, "x.y") != nil {
		t.Errorf("paths: %v", m)
	}
	if v := validationStep([]validation.Stage{{Errors: []contract.ValidationError{{Code: "CONFIG_REF_UNVERIFIED"}}}}); v.Status != fleet.StepStatusUnknown {
		t.Errorf("errors: %+v", v)
	}
	if pathOfContract(&contract.Contract{}, "service.summary") != "" || pathOfContract(&contract.Contract{}, "nothing") != "" {
		t.Error("pathOfContract")
	}
}

// TestTemplateDeliveryAddsMissingEnvironments: with --delivery, each --env
// the golden path does not declare is added with an empty values file; the
// ones it declares stay as the path wrote them.
func TestTemplateDeliveryAddsMissingEnvironments(t *testing.T) {
	text := pathContract + "environments:\n  - {name: test, file: environments/test.yaml, lifetime: persistent}\n"
	fsys := goldenPath(text)
	fsys["environments/test.yaml"] = &fstest.MapFile{Data: []byte("default: {size: 2}\n")}
	r, c := mustFromPath(t, fsys, Options{Delivery: true, Environments: []string{"test", "pr-1"}}, override.Overrides{})
	want := []contract.Environment{{Name: "test", File: "environments/test.yaml", Lifetime: contract.EnvironmentPersistent}, {Name: "pr-1", File: "environments/pr-1.yaml"}}
	if !slices.Equal(namesOf(c.Environments), namesOf(want)) || c.Environments[0].Lifetime != contract.EnvironmentPersistent || c.Environments[1].File != want[1].File {
		t.Fatalf("environments %+v", c.Environments)
	}
	if f := file(t, r, "environments/pr-1.yaml"); f.Content != "# Values pr-1 uses for each configuration, by configuration name.\n{}\n" {
		t.Fatalf("pr-1 file %q", f.Content)
	}
	if f := file(t, r, "environments/test.yaml"); f.Content != "default: {size: 2}\n" {
		t.Fatalf("test file %q", f.Content)
	}
}

func namesOf(envs []contract.Environment) []string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return out
}

// TestTemplateDropsEnvironmentExpiryAndOwner: the path author's expiry dates
// and owner teams do not carry over to a created service; lifetime does.
func TestTemplateDropsEnvironmentExpiryAndOwner(t *testing.T) {
	text := pathContract + "environments:\n  - name: preview\n    file: environments/preview.yaml\n    lifetime: temporary\n    expires: 2026-11-20T00:00:00Z\n    owner: {team: platform}\n"
	fsys := goldenPath(text)
	fsys["environments/preview.yaml"] = &fstest.MapFile{Data: []byte("{}\n")}
	r, c := mustFromPath(t, fsys, Options{}, override.Overrides{})
	if e := c.Environments[0]; e.Lifetime != contract.EnvironmentTemporary || e.Expires != "" || e.Owner != nil {
		t.Fatalf("environment %+v", e)
	}
	wantLines(t, "next", r.Next, "the golden path's environment expiry and owner were dropped (preview)")
	r, _ = mustFromPath(t, goldenPath(""), Options{}, override.Overrides{})
	if hasLine(r.Next, "environment expiry and owner were dropped") {
		t.Fatalf("nothing dropped: %v", r.Next)
	}
}
