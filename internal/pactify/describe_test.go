package pactify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// invoicesContract is what init --describe-only writes for testdata/invoices,
// the service of the blind agent evaluation: its probe becomes the health
// check, LEDGER_URL in its ConfigMap the ledger dependency and its
// environment the configuration, with no publish section.
const invoicesContract = `# Written by pacto init from Dockerfile, k8s/configmap.yaml, k8s/deployment.yaml, k8s/service.yaml.
# Check it, then validate it: pacto validate
pactoVersion: "2.1"
service:
  name: invoices
  version: 1.3.0
  owner:
    team: billing
workload: service
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
capabilities:
  - type: health
    binding:
      type: http
      interface: api
      path: /healthz
dependencies:
  # from k8s/configmap.yaml, env LEDGER_URL
  # no contract for ledger was read (pass its folder with --fleet): ref is where
  # it would be published and ^1.0.0 is a placeholder; set the versions invoices accepts
  - name: ledger
    ref: oci://ghcr.io/acme/contracts/ledger
    required: true
    compatibility: '^1.0.0'
configurations:
  - name: env
    schema: configuration/schema.json
    required: true
`

const invoicesSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "DEFAULT_CURRENCY": {
      "default": "EUR",
      "type": "string"
    },
    "LEDGER_TIMEOUT_MS": {
      "default": 2000,
      "type": "integer"
    },
    "LEDGER_URL": {
      "default": "http://ledger.finance.svc.cluster.local",
      "type": "string"
    },
    "PORT": {
      "default": 8080,
      "type": "integer"
    }
  }
}
`

// TestDescribeOnlyInvoices: --describe-only reads the ConfigMap, the env, the
// probes and the URLs into the contract, writes no publish section, no
// .pactoignore and no signing step, and what it applies validates.
func TestDescribeOnlyInvoices(t *testing.T) {
	dir := fixtureDir(t, "invoices")
	r, err := Init(context.Background(), Options{Dir: dir, DescribeOnly: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; text != invoicesContract {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if s := file(t, r, configSchema).Content; s != invoicesSchema {
		t.Fatalf("%s:\n%s", configSchema, s)
	}
	for _, f := range r.Files {
		if f.Path == ".pactoignore" {
			t.Fatalf("describe-only plans %s", f.Path)
		}
	}
	if len(r.Next) != 0 || !hasLine(r.Found, "owner team billing") || !hasLine(r.Found, "health check GET /healthz on api, from the readiness probe") ||
		!hasLine(r.Found, "the 4 environment variables invoices reads, with the values in k8s/configmap.yaml, k8s/deployment.yaml as defaults") ||
		!hasLine(r.Unknown, "compatibility ^1.0.0 in pacto.yaml is a placeholder") {
		t.Fatalf("findings: %+v", r)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if v := validation.Validate(c, raw, os.DirFS(dir)); !v.IsValid() || c.Publish != nil {
		t.Fatalf("the applied contract: %+v", v.Errors)
	}
}

// TestPublishNamesDescribeOnly: the default still writes publish, and says
// how to leave it out.
func TestPublishNamesDescribeOnly(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file(t, r, "pacto.yaml").Content, "\npublish:\n") || !hasLine(r.Next, "run pacto init --describe-only instead") || !hasLine(r.Next, "signing key") {
		t.Fatalf("plan: %+v", r)
	}
}

// TestDependencyFromFleet: a dependency whose contract is in --fleet gets
// its bundle and the range from its highest version; one whose contract has
// no publish section keeps the repository it would be published at.
func TestDependencyFromFleet(t *testing.T) {
	fleetDir := writeTree(t, t.TempDir(), map[string]string{
		"ledger-old/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: ledger, version: 1.2.0}\npublish: {bundle: oci://old.example/ledger, sign: {key: k}}\n",
		"ledger/pacto.yaml":     "pactoVersion: \"2.1\"\nservice: {name: ledger, version: 1.6.2}\npublish: {bundle: oci://reg.example/finance/ledger, sign: {key: k}}\n",
		"ledger-x/pacto.yaml":   "pactoVersion: \"2.1\"\nservice: {name: ledger, version: not-semver}\n",
	})
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, FleetRoots: []string{fleetDir}, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	if !strings.Contains(text, "  # from k8s/configmap.yaml, env LEDGER_URL; its contract in --fleet is at 1.6.2\n  - name: ledger\n    ref: oci://reg.example/finance/ledger\n    required: true\n    compatibility: '^1.6.2'\n") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if !hasLine(r.Found, "invoices calls ledger (k8s/configmap.yaml, env LEDGER_URL), whose contract in --fleet is at 1.6.2") ||
		!hasLine(r.Next, "pacto.yaml declares ledger compatible with ^1.6.2") || hasLine(r.Unknown, "placeholder") {
		t.Fatalf("findings: %+v", r)
	}

	noPublish := writeTree(t, t.TempDir(), map[string]string{"ledger/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: ledger, version: 2.0.0}\n"})
	r, err = Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, FleetRoots: []string{noPublish}, Plan: true}, os.WriteFile)
	if err != nil || !strings.Contains(file(t, r, "pacto.yaml").Content, "    ref: oci://ghcr.io/acme/contracts/ledger\n    required: true\n    compatibility: '^2.0.0'\n") {
		t.Fatalf("no publish section: %v\n%s", err, file(t, r, "pacto.yaml").Content)
	}

	other := writeTree(t, t.TempDir(), map[string]string{"billing/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: billing, version: 2.0.0}\n"})
	r, err = Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, FleetRoots: []string{other}, Plan: true}, os.WriteFile)
	if err != nil || !hasLine(r.Unknown, "no contract for ledger was read") {
		t.Fatalf("ledger not in the fleet: %+v %v", r, err)
	}
}

// TestEnvironmentAndProbes: every way a workload gets its environment, and
// every kind of probe.
func TestEnvironmentAndProbes(t *testing.T) {
	deploy := `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  template:
    spec:
      containers:
        - name: sidecar
          image: reg.io/side:1
          readinessProbe: {httpGet: {path: /side}}
        - name: api
          image: reg.io/api:1.0.0
          envFrom:
            - {configMapRef: {name: cm}, prefix: APP_}
            - {configMapRef: {name: gone}}
            - {secretRef: {name: creds}}
          env:
            - {name: APP_MODE, value: override}
            - {name: DEBUG, value: "true"}
            - {name: KEYED, valueFrom: {configMapKeyRef: {name: cm, key: MODE}}}
            - {name: MISSING, valueFrom: {configMapKeyRef: {name: gone, key: X}}}
            - {name: TOKEN, valueFrom: {secretKeyRef: {name: creds, key: token}}}
            - {name: NODE, valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
          livenessProbe: {tcpSocket: {port: 8080}}
`
	dir := writeTree(t, t.TempDir(), map[string]string{
		"deploy.yaml": deploy,
		"cm.yaml":     "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata: {MODE: fast, RETRIES: 3, EMPTY: null}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, DescribeOnly: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	if !strings.Contains(text, "capabilities:\n  - type: health\nconfigurations:") || !hasLine(r.Found, "health check, from the liveness probe in deploy.yaml") {
		t.Fatalf("a TCP probe is a health check bound to nothing:\n%s\n%v", text, r.Found)
	}
	schema := file(t, r, configSchema).Content
	for _, want := range []string{
		`"APP_MODE": {` + "\n      \"default\": \"override\"", `"APP_RETRIES": {` + "\n      \"default\": 3,\n      \"type\": \"integer\"",
		`"DEBUG": {` + "\n      \"default\": true,\n      \"type\": \"boolean\"", `"KEYED": {` + "\n      \"default\": \"fast\"",
		`"MISSING": {` + "\n      \"type\": \"string\"", `"NODE": {` + "\n      \"type\": \"string\"",
		`"TOKEN": {` + "\n      \"description\": \"Read from the Secret creds, whose values pacto init does not read.\"",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema lacks %q:\n%s", want, schema)
		}
	}
	if strings.Contains(schema, "EMPTY") {
		t.Errorf("a null ConfigMap value is a variable:\n%s", schema)
	}
	if !hasLine(r.Unknown, "the environment deploy.yaml reads from the Secret creds: its keys are not read") ||
		!hasLine(r.Unknown, "the ConfigMap gone, which deploy.yaml reads its environment from, is not in the folder") {
		t.Fatalf("unknown: %v", r.Unknown)
	}

	// An HTTP probe with no OpenAPI file is a check bound to no interface; a
	// path the binding cannot take is read as no path.
	for probe, want := range map[string]string{
		"readinessProbe: {httpGet: {path: /ready}}": "health check GET /ready, from the readiness probe",
		"startupProbe: {httpGet: {path: ready}}":    "health check, from the startup probe",
	} {
		d := writeTree(t, t.TempDir(), map[string]string{"d.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: w}\nspec:\n  template:\n    spec:\n      containers:\n        - name: w\n          image: reg.io/w:1.0.0\n          " + probe + "\n"})
		r, err := Init(context.Background(), Options{Dir: d, DescribeOnly: true, Plan: true}, os.WriteFile)
		if err != nil || !hasLine(r.Found, want) || strings.Contains(file(t, r, "pacto.yaml").Content, "binding:") {
			t.Fatalf("%s: %+v %v", probe, r, err)
		}
		if strings.Contains(probe, "/ready") && !hasLine(r.Unknown, "the interface that serves the health check GET /ready") {
			t.Fatalf("unknown: %v", r.Unknown)
		}
	}
}

// TestDescribeOnlyRefusalsAndExisting: describe-only refuses what needs a
// publish section, keeps a configuration schema already there and says
// nothing about a missing publish section in the user's own contract.
func TestDescribeOnlyRefusalsAndExisting(t *testing.T) {
	for opts, want := range map[*Options]string{
		{Delivery: true}:          "delivery ships what publish builds",
		{NoTransparencyLog: true}: "transparencyLog goes under publish.sign",
	} {
		opts.Dir, opts.DescribeOnly = fixtureDir(t, "invoices"), true
		if _, err := Init(context.Background(), *opts, os.WriteFile); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}

	dir := fixtureDir(t, "invoices")
	writeTree(t, dir, map[string]string{configSchema: "{}"})
	r, err := Init(context.Background(), Options{Dir: dir, DescribeOnly: true, Plan: true}, os.WriteFile)
	if err != nil || file(t, r, configSchema).Action != Keep {
		t.Fatalf("a schema already there: %+v %v", r, err)
	}

	own := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: p, version: 1.0.0}\n"})
	if r, err := Init(context.Background(), Options{Dir: own, DescribeOnly: true, Plan: true}, os.WriteFile); err != nil || hasLine(r.Next, "no publish section") {
		t.Fatalf("the user's own contract: %+v %v", r, err)
	}
}

// TestOwnerHint: a team/ prefix is named, and the bare name suggested.
func TestOwnerHint(t *testing.T) {
	for team, want := range map[string]string{
		"team/billing": `owner "team/billing" starts with team/: owner.team is the team's bare name, so write "billing"`,
		"Teams/Risk":   `owner "Teams/Risk" starts with teams/: owner.team is the team's bare name, so write "Risk"`,
		"billing":      "",
		"acme/billing": "",
		"team/":        "",
	} {
		if got := OwnerHint(team); got != want {
			t.Errorf("OwnerHint(%q) = %q, want %q", team, got, want)
		}
	}
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), Owner: "team/billing", DescribeOnly: true, Plan: true}, os.WriteFile)
	if err != nil || !hasLine(r.Next, `so write "billing"`) {
		t.Fatalf("init does not name the owner: %+v %v", r, err)
	}
}
