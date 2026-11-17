package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var detectedAsOf = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

// detectedRows are a registry row with its hint and a partial nodes row.
func detectedRows() []fleet.Provider {
	return detect.ProviderRows(detect.Result{SchemaVersion: "1", AsOf: detectedAsOf, Entries: []detect.Entry{
		{Kind: "registry", Value: "ghcr.io/acme", Evidence: "in 9 of 11 namespaces", Completeness: detect.Complete, Hint: "oci://ghcr.io/acme/<service>:<version>", Scope: fleet.ScopeCluster},
		{Kind: "nodes", Value: "kernel 6.1 on 6 of 6", Completeness: detect.Partial, Reason: "not confirmed on the node", Scope: fleet.ScopeCluster},
	}}, "detection", detectedAsOf)
}

func statusText(t *testing.T, res *fleet.StatusResult) string {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	expect(t, printFleetStatus(cmd, res, "text") == nil, "print")
	return out.String()
}

func TestFleetStatusTextPrintsDetectedBlock(t *testing.T) {
	out := statusText(t, &fleet.StatusResult{Providers: append(detectedRows(), crdProvider())})
	for _, want := range []string{
		"Detected (inferred, as of 2026-11-16T09:00:00Z, nothing turned on):\n",
		"  Registry: ghcr.io/acme in 9 of 11 namespaces (from detection) [available]\n",
		"    oci://ghcr.io/acme/<service>:<version>\n",
		"  Nodes: kernel 6.1 on 6 of 6 (from detection) [partial]\n",
	} {
		expect(t, strings.Contains(out, want), "status text lacks %q:\n%s", want, out)
	}
	expect(t, !strings.Contains(out, "Pacto CRD"), "a row a source reported directly is not detected:\n%s", out)
}

func TestFleetStatusTextNoDetectedBlockWithoutRows(t *testing.T) {
	out := statusText(t, &fleet.StatusResult{Providers: []fleet.Provider{crdProvider()}})
	expect(t, !strings.Contains(out, "Detected"), "no detected rows prints no block:\n%s", out)
}

func TestFleetProvidersTextMarksInferred(t *testing.T) {
	out, _ := providersText(t, &fleet.ProvidersResult{Providers: append(detectedRows(), crdProvider())})
	expect(t, strings.Contains(out, "Registry (inferred)") && strings.Contains(out, "Nodes (inferred)"), "detected rows carry the mark:\n%s", out)
	expect(t, !strings.Contains(out, "Pacto CRD (inferred)"), "a reported row carries no mark:\n%s", out)
	signIn, _ := providersText(t, &fleet.ProvidersResult{Providers: detect.ProviderRows(detect.Result{AsOf: detectedAsOf, Entries: []detect.Entry{
		{Kind: "sign-in", Value: "Dex", Completeness: detect.Complete}}}, "detection", detectedAsOf)})
	expect(t, strings.Contains(signIn, "proposal for dashboard sign-in; set it yourself"), "a detected row keeps its sentence:\n%s", signIn)
}

func TestFleetStatusTextDetectedWithoutAsOf(t *testing.T) {
	out := statusText(t, &fleet.StatusResult{Providers: []fleet.Provider{{Name: "Cloud", Evidence: "GKE", Status: fleet.SourceAvailable, Provenance: fleet.ProvenanceInferred}}})
	expect(t, strings.Contains(out, "Detected (inferred, as of unknown, nothing turned on):\n  Cloud: GKE [available]\n"), "status text:\n%s", out)
}
