package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
)

// runLocal runs pacto with a registry client that is never called, so local
// refs resolve and nothing reaches the network.
func runLocal(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCommand(app.NewService(&testutil.MockBundleStore{}, nil), VersionInfo{Version: "dev"}, defaultDeps())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	err := Execute(context.Background(), root, args)
	return out.String(), err
}

// offeringTree writes the tiered offering postgresql (storageGB declares no
// tier, so it is contract) and orders, whose production environment changes
// storageGB, and returns the root.
func offeringTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, data := range map[string]string{
		"pg/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: postgresql\n  version: 16.1.0\n  offering: postgresql\n  owner:\n    team: team-data\n" +
			"configurations:\n  - {name: default, schema: schema.json, required: false}\n",
		"pg/schema.json": `{"type":"object","additionalProperties":false,"properties":{"class":{"enum":["small","large"],"x-pacto-tier":"tunable"},"storageGB":{"type":"integer","minimum":10}}}`,
		"orders/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: orders\n  version: 1.0.0\n" +
			"configurations:\n  - name: database\n    ref: ../pg\n    required: true\n    values: {class: small, storageGB: 20}\n" +
			"environments:\n  - {name: staging, file: environments/staging.yaml}\n  - {name: production, file: environments/production.yaml}\n",
		"orders/environments/staging.yaml":    "database: {class: large}\n",
		"orders/environments/production.yaml": "database:\n  class: large\n  storageGB: 100\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const tierMessage = "environments[1].file: environment production: database.storageGB is contract-tier in offering postgresql 16.1.0, so it cannot change per environment. " +
	"Next: set it once in configurations[0].values, or ask team-data to mark it x-pacto-tier: tunable"

// TestValidateRefusesTierNotTunableOffline: pacto validate resolves a local
// offering with no network and refuses an environment that changes a
// contract-tier field, in the row format and in JSON with the code.
func TestValidateRefusesTierNotTunableOffline(t *testing.T) {
	statsOff(t)
	dir := filepath.Join(offeringTree(t), "orders")
	out, err := runLocal(t, "validate", dir)
	if err == nil || !strings.Contains(out, "environments/production.yaml:3:3: ERROR [TierNotTunable] "+tierMessage+"\n") {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
	out, _ = runLocal(t, "validate", dir, "--output-format", "json")
	var doc struct {
		Stages []struct {
			Errors []struct{ Code, Path, Message string }
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got []string
	for _, st := range doc.Stages {
		for _, e := range st.Errors {
			got = append(got, e.Code+" "+e.Path+": "+e.Message)
		}
	}
	if len(got) != 1 || got[0] != "TierNotTunable "+tierMessage {
		t.Fatalf("errors = %q", got)
	}
}

// TestCIFailsOnParameterCode: pacto ci blocks the folder, and its row carries
// the same path and message pacto validate reports with the code.
func TestCIFailsOnParameterCode(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	out, err := runLocal(t, "ci", offeringTree(t), "--output-format", "json")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code == 0 {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
	var plan struct {
		Rows []struct {
			Dir    string
			Errors []string
			Impact struct{ Gate, Text string }
		}
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, r := range plan.Rows {
		if r.Dir != "orders" {
			continue
		}
		if r.Impact.Gate != "blocked" || len(r.Errors) != 1 || r.Errors[0] != tierMessage {
			t.Fatalf("orders = %+v", r)
		}
		return
	}
	t.Fatalf("no orders row in %s", out)
}
