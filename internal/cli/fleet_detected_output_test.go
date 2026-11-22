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
		{Kind: "cloud", Completeness: detect.Unknown, Reason: "with controller.watchNamespaces the operator may not list nodes"},
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
		"  Nodes: kernel 6.1 on 6 of 6 (from detection) [partial: not confirmed on the node]\n",
		"  Cloud: (from detection) [unavailable: with controller.watchNamespaces the operator may not list nodes]\n",
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

// advisorySource is a detection source Pacto could not read.
func advisorySource() []fleet.SourceState {
	return []fleet.SourceState{{ID: "detection", Kind: fleet.SourceKindDetection, Status: fleet.SourceUnavailable, Advisory: true,
		Error: &fleet.SourceError{Code: "NOT_FOUND", Message: "not found: the operator has not published detection yet"}}}
}

// TestUnreadDetectionIsNamedInText: a detection source Pacto could not read
// is named in fleet status, fleet providers and fleet overview text, never
// read as nothing detected.
func TestUnreadDetectionIsNamedInText(t *testing.T) {
	const want = "Detected: detection not read (not found: the operator has not published detection yet)\n"
	status := statusText(t, &fleet.StatusResult{Meta: fleet.Meta{Sources: advisorySource()}})
	expect(t, strings.Contains(status, want), "status text:\n%s", status)
	providers, _ := providersText(t, &fleet.ProvidersResult{Meta: fleet.ProductMeta{Sources: advisorySource()}})
	expect(t, strings.Contains(providers, want) && !strings.Contains(providers, "add --k8s"), "providers text:\n%s", providers)
	stale := advisorySource()
	stale[0].Status, stale[0].Error = fleet.SourceStale, nil
	var overview bytes.Buffer
	printIncompleteSources(&overview, stale, false)
	expect(t, overview.String() == "Detected: detection stale\n", "overview text:\n%s", overview.String())
}

// TestFleetStatusPrintsInstallation: the installation is one declared line
// before the inferred block; the Pull Secret proposal stays inferred, with
// its hint.
func TestFleetStatusPrintsInstallation(t *testing.T) {
	rows := detect.ProviderRows(detect.Result{SchemaVersion: "1", AsOf: detectedAsOf,
		Installation: &detect.Installation{Registry: "ghcr.io/acme", Targets: "ghcr.io/acme/pacto-targets", Pushes: []string{"ghcr.io/acme/pacto-targets", "quay.io/stats/pacto"},
			LastPush: &detect.PushResult{At: detectedAsOf, Ref: "ghcr.io/acme/pacto-targets:cluster-uid"}},
		Entries: []detect.Entry{{Kind: "pull-secret", Value: "shop/ghcr-pull", Evidence: "named in the pod specs of 3 running workloads that pulled from ghcr.io",
			Completeness: detect.Complete, Hint: "--set registryPullSecret=shop/ghcr-pull", Namespaces: []string{"shop"}}}}, "detection", detectedAsOf)
	out := statusText(t, &fleet.StatusResult{Providers: rows})
	for _, want := range []string{
		"Installation: registry ghcr.io/acme; tags only under the listed prefixes, referrers by digest elsewhere (declared, as of 2026-11-16T09:00:00Z) [available]\n",
		"    target snapshot ghcr.io/acme/pacto-targets\n",
		"    push prefixes ghcr.io/acme/pacto-targets, quay.io/stats/pacto\n",
		"Detected (inferred, as of 2026-11-16T09:00:00Z, nothing turned on):\n  Pull Secret: shop/ghcr-pull named in the pod specs of",
		"    --set registryPullSecret=shop/ghcr-pull\n",
	} {
		expect(t, strings.Contains(out, want), "status text lacks %q:\n%s", want, out)
	}
	table, _ := providersText(t, &fleet.ProvidersResult{Providers: rows})
	expect(t, strings.Contains(table, "Installation (declared)") && strings.Contains(table, "Pull Secret (inferred)"), "providers table:\n%s", table)
	// An operator that advertises its profile: the line names it first.
	profiled := detect.ProviderRows(detect.Result{SchemaVersion: "1", AsOf: detectedAsOf,
		Installation: &detect.Installation{Profile: []string{"hardened"}, Registry: "ghcr.io/acme"}}, "detection", detectedAsOf)
	out = statusText(t, &fleet.StatusResult{Providers: profiled})
	expect(t, strings.Contains(out, "Installation: profile hardened; registry ghcr.io/acme;"), "status text:\n%s", out)
}
