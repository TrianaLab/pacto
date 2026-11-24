package detect

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// FluxInstallHint is the one proposal an Applier row carries: it names a
// value of the Pacto release the person already runs and never a separate
// install. Every reader shows this constant whatever a detected row says (see
// [ProviderRows]); <release>, <version> and <namespace> stay placeholders
// because the operator does not know who installed it under which name.
// --version keeps the release at the chart version it runs: without it Helm
// upgrades to the newest chart, possibly a new major.
const FluxInstallHint = "Set idp.flux.install=true on this Pacto release, at the chart version it runs: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator" +
	" --version <version> -n <namespace> --reset-then-reuse-values --set idp.flux.install=true"

// IDPProfile reports whether the profile names idp without hardened: the only
// profile whose chart can install Flux.
func IDPProfile(profile []string) bool {
	return slices.Contains(profile, "idp") && !slices.Contains(profile, "hardened")
}

// Applier is the entry that says no GitOps engine API is served (fluxServed is
// any Flux applier API: Kustomization or HelmRelease), so nothing
// applies what Pacto delivery promotes; under idp it proposes
// [FluxInstallHint]. A served engine gives no entry (its own row speaks); a
// failed discovery is unknown, never "none found".
func Applier(fluxServed, argoServed bool, err error, idp bool) (Entry, bool) {
	switch {
	case err != nil:
		return discoveryFailed(fleet.ProviderApplier, err), true
	case fluxServed || argoServed:
		return Entry{}, false
	}
	e := Entry{Kind: fleet.ProviderApplier, Value: "none found", Source: "API discovery", Completeness: Complete,
		Evidence: "none of kustomize.toolkit.fluxcd.io/v1, helm.toolkit.fluxcd.io/v2 and argoproj.io/v1alpha1 is served; no applier: install Flux or Argo CD"}
	if idp {
		e.Hint = FluxInstallHint
	}
	return e, true
}
