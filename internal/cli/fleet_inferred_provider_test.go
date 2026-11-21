package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// inferredProviderFixture publishes stripe's inferred contract to an
// in-memory registry and returns the fleet args that read it beside a local
// folder whose pay-agent pins it with ^0.0.1, and the registry store.
func inferredProviderFixture(t *testing.T) ([]string, oci.BundleStore) {
	t.Helper()
	host, key := inferredRegistry(t)
	root := writeExternalFixture(t)
	if _, _, err := execFleet(t, "fleet", "publish-inferred", "--registry", host+"/demo", "--key", key, "--local", root); err != nil {
		t.Fatalf("publish-inferred: %v", err)
	}
	repo := host + "/demo/pacto-inferred/stripe"
	mustWrite(t, filepath.Join(root, "pay-agent", "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: pay-agent
  version: "1.0.0"
dependencies:
  - name: stripe-pinned
    ref: oci://`+repo+`
    compatibility: ^0.0.1
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	return []string{"--local", root, "--oci", repo}, oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(host))
}

// TestFleetGetExternalProviderJSON is the story's UX: the provider of
// external:stripe reads unknown + INFERRED_PROVIDER, and so does the edge of
// the consumer that pins it, the one the gates read.
func TestFleetGetExternalProviderJSON(t *testing.T) {
	src, store := inferredProviderFixture(t)
	out, _, err := execFleetStore(t, store, append([]string{"fleet", "get", "external:stripe", "--output-format", "json"}, src...)...)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	var v struct {
		External struct {
			Provider struct{ RangeStatus, RangeReason, Repository string } `json:"provider"`
		} `json:"external"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if p := v.External.Provider; p.RangeStatus != "unknown" || p.RangeReason != "INFERRED_PROVIDER" || !strings.HasSuffix(p.Repository, "/demo/pacto-inferred/stripe") {
		t.Errorf("provider = %+v\n%s", p, out)
	}
	out, _, err = execFleetStore(t, store, append([]string{"fleet", "get", "pay-agent", "--output-format", "json"}, src...)...)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	var sv fleet.ServiceView
	if err := json.Unmarshal([]byte(out), &sv); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	var edge *fleet.Relationship
	for i, d := range sv.Dependencies {
		if d.To == "stripe-pinned" {
			edge = &sv.Dependencies[i]
		}
	}
	if edge == nil || edge.ToExternal != "stripe" || edge.RangeStatus != fleet.RangeUnknown || edge.RangeReason != fleet.RangeReasonInferredProvider {
		t.Errorf("edge = %+v\n%s", edge, out)
	}
}

// TestFleetGetExternalProviderText: the text names the repository and the
// shown version, says its signature could not be checked (never "unsigned":
// no check said so), that a range is never checked against it and when the
// surface was computed.
func TestFleetGetExternalProviderText(t *testing.T) {
	src, store := inferredProviderFixture(t)
	out, _, err := execFleetStore(t, store, append([]string{"fleet", "get", "external:stripe"}, src...)...)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	for _, want := range []string{"  inferred contract\n", "/demo/pacto-inferred/stripe:0.0.1 (sha256:", "    signature could not be checked, publisher unknown\n",
		"    range checks: unverified (INFERRED_PROVIDER)\n", "    surface as of ", ": hosts 0, ports 0, clients 0\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// TestFleetAttentionPrintsDeclareNext: the INFERRED_PROVIDER item of an
// external says the step that exists today, a hand-written contract (an
// edit), never pacto declare, which reads only workloads.
func TestFleetAttentionPrintsDeclareNext(t *testing.T) {
	src, store := inferredProviderFixture(t)
	out, _, err := execFleetStore(t, store, append([]string{"fleet", "attention", "--category", "knowledge"}, src...)...)
	if err != nil || !strings.Contains(out, "1 service checks a range against stripe's inferred contract") ||
		!strings.Contains(out, "(action: edit-contract via pacto_edit)") || strings.Contains(out, "pacto declare") ||
		!strings.Contains(out, "Write a contract with service.external: stripe") {
		t.Errorf("attention = %v:\n%s", err, out)
	}
}
