package detect

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// FluxInstallHint is the one proposal an Applier row carries: it names a
// value of the Pacto release the person already runs and never a separate
// install. Every reader shows this constant whatever a detected row says (see
// [ProviderRows]); <release> and <namespace> stay placeholders because the
// operator does not know who installed it under which name.
const FluxInstallHint = "Set idp.flux.install=true on this Pacto release: helm upgrade <release> oci://ghcr.io/trianalab/charts/pacto-operator" +
	" -n <namespace> --reuse-values --set idp.flux.install=true"

// IDPProfile reports whether the profile names idp without hardened: the only
// profile whose chart can install Flux.
func IDPProfile(profile []string) bool {
	return slices.Contains(profile, "idp") && !slices.Contains(profile, "hardened")
}

// Applier is the entry that says no GitOps engine API is served, so nothing
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
		Evidence: "neither kustomize.toolkit.fluxcd.io/v1 nor argoproj.io/v1alpha1 is served; no applier: install Flux or Argo CD"}
	if idp {
		e.Hint = FluxInstallHint
	}
	return e, true
}
