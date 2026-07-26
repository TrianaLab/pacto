//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/internal/loader"
	"github.com/trianalab/pacto/v2/pkg/dashboard"
	"github.com/trianalab/pacto/v2/pkg/oci"
)

// port is the Service port every fixture uses; the collector resolves probe targets to it.
const port int32 = 8080

// --- contract builder -------------------------------------------------------

type ifaceSpec struct {
	name string
	typ  string // default "openapi"
}

type capSpec struct {
	typ   string // "health" | "metrics" | "extension"
	iface string // owning interface name; "" => no binding (standard)
	path  string // optional application path
	ref   string // extension only
}

type depSpec struct {
	name, ref, compat string
	required          bool
}

type cfgSpec struct {
	name, schema string
	required     bool
}

type contractSpec struct {
	name        string
	workload    string // "", "service", "job", "scheduled"
	persistence string // "", "ephemeral", "persistent"
	ifaces      []ifaceSpec
	caps        []capSpec
	deps        []depSpec
	cfgs        []cfgSpec
	conformance []string
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// buildContract renders a minimal, schema-valid pactoVersion 2.0 inline contract from the spec.
func buildContract(s contractSpec) string {
	var b strings.Builder
	b.WriteString("pactoVersion: \"2.0\"\n")
	b.WriteString("service:\n  name: " + s.name + "\n  version: 1.0.0\n")
	if s.workload != "" {
		b.WriteString("workload: " + s.workload + "\n")
	}
	if s.persistence != "" {
		b.WriteString("state:\n  type: stateful\n  dataCriticality: low\n  persistence:\n    scope: local\n    durability: " + s.persistence + "\n")
	}
	if len(s.ifaces) > 0 {
		b.WriteString("interfaces:\n")
		for _, i := range s.ifaces {
			typ := i.typ
			if typ == "" {
				typ = "openapi"
			}
			b.WriteString("  - name: " + i.name + "\n    type: " + typ + "\n    ref: " + i.name + ".spec\n")
		}
	}
	if len(s.caps) > 0 {
		b.WriteString("capabilities:\n")
		for _, c := range s.caps {
			b.WriteString("  - type: " + c.typ + "\n")
			if c.ref != "" {
				b.WriteString("    ref: " + c.ref + "\n")
			}
			if c.iface != "" {
				b.WriteString("    binding:\n      type: http\n      interface: " + c.iface + "\n")
				if c.path != "" {
					b.WriteString("      path: '" + c.path + "'\n")
				}
			}
		}
	}
	if len(s.deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range s.deps {
			b.WriteString("  - name: " + d.name + "\n    ref: " + d.ref + "\n    required: " + boolStr(d.required) + "\n    compatibility: " + d.compat + "\n")
		}
	}
	if len(s.cfgs) > 0 {
		b.WriteString("configurations:\n")
		for _, cf := range s.cfgs {
			b.WriteString("  - name: " + cf.name + "\n    required: " + boolStr(cf.required) + "\n")
			if cf.schema != "" {
				b.WriteString("    schema: '" + cf.schema + "'\n")
			}
		}
	}
	if len(s.conformance) > 0 {
		b.WriteString("verification:\n  conformance:\n")
		for _, n := range s.conformance {
			b.WriteString("    - " + n + "\n")
		}
	}
	return b.String()
}

// apiBinding is the standard interface->port binding used across cases.
func apiBinding(iface string) []pactov1alpha1.InterfaceBinding {
	return []pactov1alpha1.InterfaceBinding{{Interface: iface, ServicePort: intstr.FromInt32(port)}}
}

// =====================================================================================
// workload (Refinement C / AR7)
// =====================================================================================

func TestWorkload(t *testing.T) {
	t.Run("explicit_name_and_kind_match_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "service"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	t.Run("explicit_wrong_kind_noncompliant_WORKLOAD_MISMATCH", func(t *testing.T) {
		ns := newNamespace(t)
		createJob(t, ns, "wl")
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "service"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Job"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "WORKLOAD_MISMATCH")
	})

	t.Run("defaulted_kind_wrong_type_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{}) // observed type "service"
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			// contract wants a job; kind is left unspecified (defaulted to Deployment for the GET only).
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "job"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "WORKLOAD_MISMATCH")
	})

	t.Run("notfound_unknown_EVIDENCE_MISSING", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "service"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "ghost", Kind: "Deployment"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_MISSING")
	})

	t.Run("api_failure_unknown_COLLECTION_FAILED", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "service"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"}},
		})
		// Full real pipeline, but the apiserver GET of this Deployment errors (non-NotFound).
		p := reconcile(t, "p", ns, reconcileOpts{cl: faultClient{Client: k8sClient, failDeploymentName: "wl"}}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "COLLECTION_FAILED")
	})

	t.Run("service_name_only_defaults_to_deployment_compliant", func(t *testing.T) {
		// ServiceName-only target -> ResolvedWorkload returns {name:svc, kind:Deployment}, exercising the
		// ServiceName->Deployment resolution branch (distinct from the WorkloadRef rows above).
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "service"})},
			Target:      pactov1alpha1.TargetRef{ServiceName: "wl"}, // no WorkloadRef -> kind defaulted, not explicit
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	t.Run("service_name_only_wrong_type_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		// ServiceName-only (kind defaulted, WorkloadExplicit=false) + observed Deployment(service) vs a
		// contract wanting job. Shares the non-explicit mismatch path with defaulted_kind_wrong_type above
		// (-> EVIDENCE_INSUFFICIENT, never WORKLOAD_MISMATCH); present as its own matrix row via ServiceName.
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", workload: "job"})},
			Target:      pactov1alpha1.TargetRef{ServiceName: "wl"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "WORKLOAD_MISMATCH")
	})
}

// =====================================================================================
// persistence (B3)
// =====================================================================================

func TestPersistence(t *testing.T) {
	pvc := []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-pvc"}}}}
	emptyDir := []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	hostPath := []corev1.Volume{{Name: "hp", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/data"}}}}

	persistentTarget := func() pactov1alpha1.TargetRef {
		return pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"}}
	}

	t.Run("binding_declared_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{volumes: pvc})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", persistence: "persistent"})},
			Target:      persistentTarget(),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	t.Run("all_ephemeral_noncompliant_PERSISTENCE_MISMATCH", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{volumes: emptyDir})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", persistence: "persistent"})},
			Target:      persistentTarget(),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "PERSISTENCE_MISMATCH")
	})

	t.Run("ambiguous_volume_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createDeployment(t, ns, "wl", deployOpts{volumes: hostPath})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", persistence: "persistent"})},
			Target:      persistentTarget(),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "PERSISTENCE_MISMATCH")
	})

	t.Run("workload_notfound_unknown_EVIDENCE_MISSING", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", persistence: "persistent"})},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "ghost", Kind: "Deployment"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_MISSING")
	})

	t.Run("ephemeral_compliant_excluded_from_coverage", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			// ephemeral persistence adds NO required assertion; only the interface is required.
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name: "app", persistence: "ephemeral", ifaces: []ifaceSpec{{name: "api"}}})},
			Target: pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1) // persistence NOT counted
	})
}

// =====================================================================================
// interfaces (B1 + B4 + B5)
// =====================================================================================

func TestInterfaces(t *testing.T) {
	ifaceTarget := pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")}

	t.Run("bound_ready_compliant_availability", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	t.Run("zero_ready_within_window_unknown", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 0) // slice exists, zero ready
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "INTERFACE_ABSENT")
	})

	t.Run("zero_ready_beyond_window_noncompliant_INTERFACE_ABSENT", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		reconcile(t, "p", ns, reconcileOpts{}) // seed window (within -> Unknown)
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "INTERFACE_ABSENT")
		requireNoIP(t, p)
	})

	t.Run("bound_service_notfound_unknown_EVIDENCE_MISSING", func(t *testing.T) {
		// A bound interface whose backing Service is absent -> the observer emits NO observation, so the engine
		// infers EVIDENCE_MISSING (spec section 7.3, mirroring the workload dimension). Honest Unknown, never a
		// false INTERFACE_ABSENT.
		ns := newNamespace(t)
		// no createService("svc") -> Service GET returns NotFound
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_MISSING")
		requireNoFinding(t, p, "INTERFACE_ABSENT")
	})

	t.Run("service_get_error_unknown_COLLECTION_FAILED", func(t *testing.T) {
		// The interface dimension reads the backing Service to count ready endpoints; a non-NotFound API error
		// on that GET -> the interface dimension is Failed -> COLLECTION_FAILED, service Unknown (never a
		// violation on a collection gap).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		p := reconcile(t, "p", ns, reconcileOpts{cl: faultClient{Client: k8sClient, failServiceName: "svc"}}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown) // Unknown, not NonCompliant
		requireFinding(t, p, "COLLECTION_FAILED")
		requireNoFinding(t, p, "INTERFACE_ABSENT")
	})

	t.Run("zero_ready_window_recovery_compliant", func(t *testing.T) {
		// Zero-ready sustained beyond the window -> confirmed INTERFACE_ABSENT; a ready endpoint then resets the
		// window and recovers to Compliant (spec section 7.3 recovery path).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      ifaceTarget,
		})
		reconcile(t, "p", ns, reconcileOpts{}) // seed window (within -> Unknown)
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "INTERFACE_ABSENT")

		// Endpoint becomes ready -> window resets and status recovers.
		setEndpointSliceReady(t, ns, "svc", port)
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("expected window reset after recovery, got %+v", p.Status.ObservationWindows)
		}
	})

	t.Run("no_binding_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc"}, // no interfaceBindings
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("conformance_optin_no_evaluator_unknown_EXTENSION_EVALUATOR_UNAVAILABLE", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1) // availability satisfied
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name: "app", ifaces: []ifaceSpec{{name: "api"}}, conformance: []string{"api"}})},
			Target: ifaceTarget,
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EXTENSION_EVALUATOR_UNAVAILABLE")
		requireNoFinding(t, p, "INTERFACE_ABSENT") // availability still satisfied
		requireCoverage(t, p, 1, 2)                // availability evaluated; conformance required-but-unevaluated
	})

	t.Run("asyncapi_no_binding_unknown", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name: "app", ifaces: []ifaceSpec{{name: "events", typ: "asyncapi"}}})},
			Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})
}

// =====================================================================================
// health capability (Refinement A + B4 + B5)
// =====================================================================================

func TestHealth(t *testing.T) {
	healthContract := buildContract(contractSpec{
		name:   "app",
		ifaces: []ifaceSpec{{name: "api"}},
		caps:   []capSpec{{typ: "health", iface: "api", path: "/healthz"}},
	})
	target := pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")}

	t.Run("direct_probe_2xx_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, okHealth)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 2, 2) // interface availability + health
	})

	t.Run("passive_tierB_readiness_ready_compliant", func(t *testing.T) {
		// EnableProbing OFF -> no active Tier-A probe. The passive Tier-B fallback reads the target-port
		// container's httpGet readiness probe plus a ready endpoint -> health satisfied (spec section 7.4).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createDeployment(t, ns, "svc", deployOpts{readinessProbePort: port}) // workload name defaults to svc
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{probingDisabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 2, 2) // interface availability + health (Tier B)
	})

	t.Run("direct_probe_2xx_body_not_persisted_canary", func(t *testing.T) {
		// The prober reads a response body for the 2xx check; that body must never reach status (INV-5).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, okHealthWithBody)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireStatusExcludes(t, p, healthBodyCanary)
	})

	t.Run("declared_path_404_beyond_window_noncompliant_CAPABILITY_ABSENT", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, status404)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		reconcile(t, "p", ns, reconcileOpts{}) // 404 within window -> Unknown
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "CAPABILITY_ABSENT")
	})

	t.Run("declared_path_404_window_recovery_compliant", func(t *testing.T) {
		// A declared-path 404 sustained beyond the window -> confirmed CAPABILITY_ABSENT; a 2xx then resets the
		// window and recovers to Compliant (spec section 7.4 recovery path).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		flip := startSwitchableProbeServer(t, ns, "svc", port, status404)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		reconcile(t, "p", ns, reconcileOpts{}) // 404 within window -> Unknown
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "CAPABILITY_ABSENT")

		// Health path starts serving 2xx -> status recovers, the confirmed CAPABILITY_ABSENT clears, and the
		// satisfied path emits a window-CLEARING update so the stale capability/health window is DELETED (I7) —
		// exactly like the interface/dependency/configuration recovery cases.
		flip(okHealth)
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("expected health window reset after recovery, got %+v", p.Status.ObservationWindows)
		}

		// I7 proof: a single declared-path 404 AFTER recovery must start a FRESH window and be windowed as
		// Unknown (EVIDENCE_INSUFFICIENT), NOT immediately confirmed CAPABILITY_ABSENT off the stale window.
		flip(status404)
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
	})

	t.Run("service_get_error_unknown_COLLECTION_FAILED", func(t *testing.T) {
		// The health path resolves the probe target by reading the backing Service; a non-NotFound API error on
		// that GET -> the health dimension is Failed -> COLLECTION_FAILED, service Unknown (never a violation on
		// a collection gap).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, okHealth)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{cl: faultClient{Client: k8sClient, failServiceName: "svc"}}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown) // Unknown, not NonCompliant
		requireFinding(t, p, "COLLECTION_FAILED")
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
	})

	t.Run("probe_5xx_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, status503) // serving but unhealthy -> present, not absent
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
	})

	t.Run("probe_unreachable_no_readiness_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createDeployment(t, ns, "svc", deployOpts{}) // no readiness probe -> no usable tier-B evidence
		pointAtClosedPort(t, ns, "svc", port)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		// Spec section 7.4: liveness-only / no-probe -> EVIDENCE_INSUFFICIENT, not COLLECTION_FAILED (M13).
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
	})

	t.Run("no_binding_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			// health capability with NO binding block -> Unsupported.
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name: "app", caps: []capSpec{{typ: "health"}}})},
			Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("undeclared_interface_invalid_CAPABILITY_INTERFACE_UNKNOWN", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name: "app", caps: []capSpec{{typ: "health", iface: "ghost", path: "/healthz"}}})},
			Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusInvalid)
		if !contains(validationCodes(p), "CAPABILITY_INTERFACE_UNKNOWN") {
			t.Fatalf("expected CAPABILITY_INTERFACE_UNKNOWN, got %v", validationCodes(p))
		}
	})
}

// =====================================================================================
// metrics capability (Refinement D)
// =====================================================================================

func TestMetrics(t *testing.T) {
	metricsContract := func(path string) string {
		return buildContract(contractSpec{
			name:   "app",
			ifaces: []ifaceSpec{{name: "api"}},
			caps:   []capSpec{{typ: "metrics", iface: "api", path: path}},
		})
	}
	target := pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")}

	t.Run("active_probe_prometheus_parsed_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, promMetrics)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("/metrics")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 2, 2)
	})

	t.Run("substring_but_not_parseable_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, substringButNotPrometheus)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("/metrics")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
	})

	t.Run("reachable_non200_never_window_never_violation", func(t *testing.T) {
		// INVARIANT LOCK (spec section 7.5 / Refinement D): metrics has NO reliable operator-side negative, so a
		// reachable non-200 is EVIDENCE_INSUFFICIENT (Unknown) forever — it must NEVER open a stabilization
		// window and NEVER advance to CAPABILITY_ABSENT. A regression that added metrics windowing would seed a
		// window on the first negative (failing the len==0 assertion below); one that added a metrics violation
		// would flip the sustained negative to NonCompliant (failing the second-cycle status assertion).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1) // interface availability satisfied -> no interface window
		startProbeServer(t, ns, "svc", port, status404)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("/metrics")}, Target: target})

		// Cycle 1: first reachable-404 negative.
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("metrics reachable non-200 must NOT open a window, got %+v", p.Status.ObservationWindows)
		}

		// Cycle 2: sustained negative stays Unknown with still no window (no path to a confirmed violation).
		p = reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("sustained metrics non-200 must still carry NO window, got %+v", p.Status.ObservationWindows)
		}
	})

	t.Run("transport_error_unknown_COLLECTION_FAILED", func(t *testing.T) {
		// A transport error reaching the discovered scrape target (connection refused) -> the metrics dimension
		// is Failed -> COLLECTION_FAILED, service Unknown (never a violation on a collection gap).
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1) // interface availability satisfied
		pointAtClosedPort(t, ns, "svc", port)      // scrape probe gets connection-refused
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("/metrics")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown) // Unknown, not NonCompliant
		requireFinding(t, p, "COLLECTION_FAILED")
		requireNoFinding(t, p, "CAPABILITY_ABSENT")
	})

	t.Run("disabled_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("/metrics")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: false}).pacto // flag OFF
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("enabled_no_discovery_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		// metrics binding without a path, no ServiceMonitor/annotation/named-port -> nothing to probe.
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("annotation_discovery_prometheus_compliant", func(t *testing.T) {
		// prometheus.io annotations carry the scrape path+port; the contract has NO metrics path, so
		// discovery MUST come from the annotations (spec section 7.5 precedence).
		ns := newNamespace(t)
		createServiceFull(t, ns, "svc",
			[]corev1.ServicePort{{Name: "http", Port: port, TargetPort: intstr.FromInt32(port)}},
			map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/path":   "/metrics",
				"prometheus.io/port":   fmt.Sprintf("%d", port),
			})
		createEndpointSlice(t, ns, "svc", port, 1)
		startProbeServer(t, ns, "svc", port, promMetrics)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 2, 2) // interface availability + metrics
	})

	t.Run("named_port_discovery_prometheus_compliant", func(t *testing.T) {
		// A Service port named "metrics" is the scrape target; the contract has NO metrics path and no
		// annotations, so discovery MUST come from the named port (spec section 7.5 precedence).
		const metricsScrapePort int32 = 9090
		ns := newNamespace(t)
		createServiceFull(t, ns, "svc",
			[]corev1.ServicePort{
				{Name: "http", Port: port, TargetPort: intstr.FromInt32(port)},                              // bound interface port
				{Name: "metrics", Port: metricsScrapePort, TargetPort: intstr.FromInt32(metricsScrapePort)}, // scrape target
			}, nil)
		createEndpointSlice(t, ns, "svc", port, 1) // availability on the interface port
		startProbeServer(t, ns, "svc", metricsScrapePort, promMetrics)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: metricsContract("")}, Target: target})
		p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: true}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 2, 2) // interface availability + metrics
	})

	t.Run("servicemonitor_discovery_unit_covered_blocked_in_envtest", func(t *testing.T) {
		// ServiceMonitor discovery (spec section 7.5, first precedence) reads monitoring.coreos.com/v1
		// ServiceMonitor CRs via an unstructured List. envtest loads only config/crd/bases, which does NOT
		// include the prometheus-operator ServiceMonitor CRD, and that CRD cannot be sourced offline from the
		// module cache (prometheus-operator is not a dependency; only ServiceMonitor *instances* live there,
		// not the CRD definition, and no network fetch is permitted in the test run). This path is therefore
		// UNIT-covered by internal/observer/metrics_discovery_test.go (TestDiscoverFromServiceMonitor_*) and
		// guarded here on CRD presence rather than silently skipped.
		if serviceMonitorCRDInstalled(testCtx) {
			t.Fatal("ServiceMonitor CRD is installed; wire a positive e2e discovery case (SM fixture + probe) here")
		}
		t.Skip("ServiceMonitor discovery needs the monitoring.coreos.com CRD, which envtest does not load and " +
			"cannot be sourced offline; unit-covered by internal/observer/metrics_discovery_test.go")
	})
}

// =====================================================================================
// extension capability (N1)
// =====================================================================================

func TestExtensionCapability(t *testing.T) {
	ns := newNamespace(t)
	createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
			name: "app", caps: []capSpec{{typ: "extension", ref: "example.com/backup"}}})},
		Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
	})
	p := reconcile(t, "p", ns, reconcileOpts{}).pacto
	requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
	requireFinding(t, p, "EXTENSION_EVALUATOR_UNAVAILABLE")
	requireNoFinding(t, p, "EVIDENCE_MISSING")
	requireCoverage(t, p, 0, 1) // required but never evaluable (no collector observation)
}

// =====================================================================================
// required-dependencies (B5)
// =====================================================================================

func TestDependencies(t *testing.T) {
	// depContract builds a contract with one dependency; ref is namespaced-unique to avoid cross-test
	// sibling matches (Pacto list is cluster-wide).
	depContract := func(ns string, required bool) string {
		return buildContract(contractSpec{
			name: "app",
			deps: []depSpec{{name: "payments", ref: "oci://ghcr.io/e2e/" + ns + "-payments", required: required, compat: "1.x"}},
		})
	}
	mainTarget := pactov1alpha1.TargetRef{ServiceName: "main-svc"}

	t.Run("ready_backend_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	t.Run("zero_ready_within_window_unknown", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
	})

	t.Run("zero_ready_beyond_window_noncompliant_DEPENDENCY_UNREACHABLE", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		reconcile(t, "p", ns, reconcileOpts{})
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "DEPENDENCY_UNREACHABLE")
		requireNoIP(t, p)
	})

	t.Run("no_sibling_external_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("sibling_service_notfound_within_window_unknown", func(t *testing.T) {
		// Sibling CR resolves to a serviceName, but the backing Service itself is absent -> a windowed
		// reachability negative (spec section 7.6). Within window -> honest Unknown, never a violation.
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		// no createService("pay-svc") -> Service GET returns NotFound
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		requireNoFinding(t, p, "DEPENDENCY_UNREACHABLE")
	})

	t.Run("sibling_service_notfound_beyond_window_noncompliant_DEPENDENCY_UNREACHABLE", func(t *testing.T) {
		// Same absent-Service negative sustained beyond the stabilization window -> confirmed unreachable.
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		reconcile(t, "p", ns, reconcileOpts{}) // seed window (within -> Unknown)
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "DEPENDENCY_UNREACHABLE")
		requireNoIP(t, p)
	})

	t.Run("sibling_externalname_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		// Sibling resolves to a type=ExternalName Service (no selector, no endpoints) -> the observer cannot
		// count readiness, so the honest subcode is Unsupported (spec section 7.6), never a violation.
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createExternalNameService(t, ns, "pay-svc", "payments.external.example.com")
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("optional_absent_no_finding_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, false)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant) // optional + unresolvable -> no false Unknown
		if len(p.Status.Findings) != 0 {
			t.Fatalf("optional absent dependency should surface no finding, got %v", findingCodes(p))
		}
	})

	t.Run("optional_beyond_window_warning", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, false)}, Target: mainTarget})
		reconcile(t, "p", ns, reconcileOpts{})
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusWarning) // optional contradiction -> Warning, not Error
		f := findFinding(p, "DEPENDENCY_UNREACHABLE")
		if f == nil || f.Severity != "warning" {
			t.Fatalf("expected optional DEPENDENCY_UNREACHABLE at warning severity, got %+v", f)
		}
	})

	t.Run("collection_failed_cycle_does_not_advance_window", func(t *testing.T) {
		// Spec section 12: a transient collection failure emits Failed WITHOUT a window update, so the
		// stabilization clock must neither advance nor reset. Seed a window, backdate it beyond the
		// boundary, run a COLLECTION_FAILED cycle (the dependency Service GET errors) and assert the
		// window is PRESERVED and the status is Unknown, then a normal reconcile confirms NonCompliant.
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 0) // sustained zero-ready negative
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})

		// 1. First negative -> within window -> Unknown; window seeded.
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")

		// 2. Backdate the window beyond the stabilization boundary.
		backdateWindows(t, "p", ns)
		seeded := getPacto(t, "p", ns).Status.ObservationWindows
		if len(seeded) != 1 {
			t.Fatalf("expected 1 seeded window, got %d", len(seeded))
		}
		backdated := seeded[0].FirstObservedNegativeAt

		// 3. COLLECTION_FAILED cycle: the dependency Service GET errors -> Failed, no window update.
		p = reconcile(t, "p", ns, reconcileOpts{cl: faultClient{Client: k8sClient, failServiceName: "pay-svc"}}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "COLLECTION_FAILED")
		if len(p.Status.ObservationWindows) != 1 {
			t.Fatalf("COLLECTION_FAILED cycle must PRESERVE the window, got %d", len(p.Status.ObservationWindows))
		}
		if !p.Status.ObservationWindows[0].FirstObservedNegativeAt.Equal(&backdated) {
			t.Fatalf("COLLECTION_FAILED cycle advanced/reset the window: was %v, now %v",
				backdated, p.Status.ObservationWindows[0].FirstObservedNegativeAt)
		}

		// 4. Normal reconcile: the sustained negative is now beyond the PRESERVED window -> confirmed.
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "DEPENDENCY_UNREACHABLE")
	})

	t.Run("window_resets_when_backend_becomes_ready", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "oci://ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 0)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: depContract(ns, true)}, Target: mainTarget})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		if len(p.Status.ObservationWindows) == 0 {
			t.Fatalf("expected a window after a negative observation")
		}
		// Backend becomes ready -> window must reset and status recover.
		setEndpointSliceReady(t, ns, "pay-svc", port)
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("expected window reset after recovery, got %+v", p.Status.ObservationWindows)
		}
	})
}

// =====================================================================================
// required-configurations (B6 + B7)
// =====================================================================================

const configSchema = `{"type":"object","required":["level"],"properties":{"level":{"type":"string"}}}`

func TestConfigurations(t *testing.T) {
	cfgContract := func(required bool, schema string) string {
		return buildContract(contractSpec{name: "app", cfgs: []cfgSpec{{name: "appcfg", required: required, schema: schema}}})
	}
	bind := func(kind, name, key, format string) pactov1alpha1.TargetRef {
		return pactov1alpha1.TargetRef{
			ServiceName:    "main-svc",
			ConfigBindings: []pactov1alpha1.ConfigBinding{{Configuration: "appcfg", Kind: kind, Name: name, Key: key, Format: format}},
		}
	}

	t.Run("configmap_conforms_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		// A distinctive but schema-valid string value (level must be a string): proves B6/B7 discard the
		// ConfigMap's VALUES even on the conforming path — only the boolean result may surface (INV-5).
		const cfgValueCanary = "prod-CONFIGVALUE-LEAK-CANARY-42"
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: " + cfgValueCanary + "\n"})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "app-cm", "config.yaml", "yaml"),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
		requireStatusExcludes(t, p, cfgValueCanary)
	})

	t.Run("configmap_get_error_unknown_COLLECTION_FAILED", func(t *testing.T) {
		// A bound ConfigMap that exists, but whose apiserver GET errors (non-NotFound) -> the config
		// dimension is Failed -> COLLECTION_FAILED, service Unknown (never NonCompliant on a collection gap).
		ns := newNamespace(t)
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: info\n"})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "app-cm", "config.yaml", "yaml"),
		})
		p := reconcile(t, "p", ns, reconcileOpts{cl: faultClient{Client: k8sClient, failConfigMapName: "app-cm"}}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown) // Unknown, not NonCompliant
		requireFinding(t, p, "COLLECTION_FAILED")
	})

	t.Run("configmap_violates_schema_noncompliant_CONFIGURATION_MISMATCH", func(t *testing.T) {
		ns := newNamespace(t)
		// level is an int, schema requires string; a distinctive extra key must NOT surface in the finding.
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: 123\nother_key: sideValue987\n"})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "app-cm", "config.yaml", "yaml"),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		f := findFinding(p, "CONFIGURATION_MISMATCH")
		if f == nil {
			t.Fatalf("expected CONFIGURATION_MISMATCH, got %v", findingCodes(p))
		}
		if strings.Contains(f.Message, "sideValue987") || strings.Contains(f.Message, "other_key") {
			t.Fatalf("mismatch message leaked non-contract ConfigMap content: %q", f.Message)
		}
	})

	t.Run("optional_present_nonconformant_warning_CONFIGURATION_MISMATCH", func(t *testing.T) {
		// An OPTIONAL configuration that is present but violates its schema is a genuine contradiction, but on an
		// optional assertion it degrades to Warning (not NonCompliant): the finding surfaces at warning severity
		// and the service status is Warning.
		ns := newNamespace(t)
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: 123\n"}) // level int, schema wants string
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(false, configSchema)}, // required: false
			Target:      bind("ConfigMap", "app-cm", "config.yaml", "yaml"),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusWarning) // optional contradiction -> Warning, not Error
		f := findFinding(p, "CONFIGURATION_MISMATCH")
		if f == nil || f.Severity != "warning" {
			t.Fatalf("expected optional CONFIGURATION_MISMATCH at warning severity, got %+v", f)
		}
	})

	t.Run("bound_configmap_missing_beyond_window_noncompliant_CONFIGURATION_ABSENT", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "missing-cm", "config.yaml", "yaml"),
		})
		reconcile(t, "p", ns, reconcileOpts{})
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "CONFIGURATION_ABSENT")
	})

	t.Run("bound_configmap_window_recovery_compliant", func(t *testing.T) {
		// A bound ConfigMap absent beyond the window -> confirmed CONFIGURATION_ABSENT; creating a present +
		// conformant ConfigMap then resets the window and recovers to Compliant (spec section 7.7 recovery path).
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "app-cm", "config.yaml", "yaml"),
		})
		reconcile(t, "p", ns, reconcileOpts{}) // absent within window -> Unknown
		backdateWindows(t, "p", ns)
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "CONFIGURATION_ABSENT")

		// The bound ConfigMap appears with conformant content -> window resets and status recovers.
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: info\n"})
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		if len(p.Status.ObservationWindows) != 0 {
			t.Fatalf("expected window reset after recovery, got %+v", p.Status.ObservationWindows)
		}
	})

	t.Run("no_binding_unknown_OBSERVATION_UNSUPPORTED", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      pactov1alpha1.TargetRef{ServiceName: "main-svc"}, // no configBindings
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "OBSERVATION_UNSUPPORTED")
	})

	t.Run("configmap_without_key_format_unknown_EVIDENCE_INSUFFICIENT", func(t *testing.T) {
		ns := newNamespace(t)
		createConfigMap(t, ns, "app-cm", map[string]string{"config.yaml": "level: info\n"})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(true, configSchema)},
			Target:      bind("ConfigMap", "app-cm", "", ""), // existence-only
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
	})

	t.Run("optional_missing_no_finding_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: cfgContract(false, configSchema)},
			Target:      bind("ConfigMap", "missing-cm", "config.yaml", "yaml"),
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant) // optional + within-window absence -> no finding
		if len(p.Status.Findings) != 0 {
			t.Fatalf("optional missing configuration should surface no finding, got %v", findingCodes(p))
		}
	})
}

// =====================================================================================
// Secret metadata-only + NO-LEAK (INV-5)
// =====================================================================================

func TestSecretNoLeak(t *testing.T) {
	ns := newNamespace(t)
	const (
		secretKey1 = "DB_PASSWORD"
		secretKey2 = "api-token"
		secretVal1 = "S3cr3t-P@ssw0rd-LEAK-CANARY"
		secretVal2 = "tok_live_LEAKCANARY_998877"
	)
	createSecret(t, ns, "creds", map[string][]byte{
		secretKey1: []byte(secretVal1),
		secretKey2: []byte(secretVal2),
	})
	createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
		// A schema is required structurally (config needs schema XOR ref); the Secret observation path is
		// metadata-only and never reads it.
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
			name: "app", cfgs: []cfgSpec{{name: "appcfg", required: true, schema: configSchema}}})},
		Target: pactov1alpha1.TargetRef{
			ServiceName:    "main-svc",
			ConfigBindings: []pactov1alpha1.ConfigBinding{{Configuration: "appcfg", Kind: "Secret", Name: "creds"}},
		},
	})
	res := reconcile(t, "p", ns, reconcileOpts{})
	p := res.pacto

	// Metadata-only existence -> Unknown, never satisfied.
	requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
	requireFinding(t, p, "EVIDENCE_INSUFFICIENT")

	// Scan the ENTIRE serialized status + every recorded event for secret values and key names.
	statusJSON, err := json.Marshal(p.Status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	haystack := string(statusJSON) + "\n" + strings.Join(res.events, "\n")
	for _, canary := range []string{secretVal1, secretVal2, secretKey1, secretKey2} {
		if strings.Contains(haystack, canary) {
			t.Fatalf("INV-5 LEAK: secret material %q found in status/events", canary)
		}
	}
}

// =====================================================================================
// SSRF rejection (Refinement F / INV-6 / AR4)
// =====================================================================================

func TestSSRFRejection(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"double_slash_authority", "//evil.example/steal"},
		{"http_scheme", "http://evil.example/steal"},
		{"https_scheme", "https://evil.example/steal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			createService(t, ns, "svc", port)
			createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
				ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
					name:   "app",
					ifaces: []ifaceSpec{{name: "api"}},
					caps:   []capSpec{{typ: "health", iface: "api", path: tc.path}},
				})},
				Target: pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")},
			})
			p := reconcile(t, "p", ns, reconcileOpts{}).pacto
			// A hostile path is rejected at validation -> Invalid, never a runtime state.
			requireStatus(t, p, pactov1alpha1.ContractStatusInvalid)
			codes := validationCodes(p)
			if !contains(codes, "CAPABILITY_PATH_INVALID") && !contains(codes, "SCHEMA_VIOLATION") {
				t.Fatalf("expected a path-rejection error (CAPABILITY_PATH_INVALID/SCHEMA_VIOLATION), got %v", codes)
			}
		})
	}

	// A schema-valid-but-crossfield-unsafe path (embedded fragment) exercises the dedicated AR4 code.
	t.Run("crossfield_fragment_CAPABILITY_PATH_INVALID", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
				name:   "app",
				ifaces: []ifaceSpec{{name: "api"}},
				caps:   []capSpec{{typ: "health", iface: "api", path: "/healthz#frag"}},
			})},
			Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusInvalid)
		if !contains(validationCodes(p), "CAPABILITY_PATH_INVALID") {
			t.Fatalf("expected CAPABILITY_PATH_INVALID, got %v", validationCodes(p))
		}
	})
}

// =====================================================================================
// coverage propagation + aggregate precedence + load failures
// =====================================================================================

func TestCoveragePropagation(t *testing.T) {
	// 6 required assertions, exactly 2 with Outcome=Observed:
	//   interface api  (bound + ready)   -> evaluated
	//   interface b    (no binding)      -> Unsupported
	//   capability health (no binding)   -> Unsupported
	//   capability metrics (disabled)    -> Unsupported
	//   dependency dep (no sibling)      -> Unsupported
	//   workload service (Deployment)    -> evaluated
	ns := newNamespace(t)
	createService(t, ns, "svc", port)
	createEndpointSlice(t, ns, "svc", port, 1)
	createDeployment(t, ns, "svc", deployOpts{})
	c := buildContract(contractSpec{
		name:     "app",
		workload: "service",
		ifaces:   []ifaceSpec{{name: "api"}, {name: "b"}},
		caps:     []capSpec{{typ: "health"}, {typ: "metrics", iface: "api"}},
		deps:     []depSpec{{name: "dep", ref: "oci://ghcr.io/e2e/" + ns + "-dep", required: true, compat: "1.x"}},
	})
	createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: c},
		Target: pactov1alpha1.TargetRef{
			ServiceName:       "svc",
			WorkloadRef:       &pactov1alpha1.WorkloadRef{Name: "svc", Kind: "Deployment"},
			InterfaceBindings: apiBinding("api"), // only api bound; b unbound
		},
	})
	p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: false}).pacto
	requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
	requireCoverage(t, p, 2, 6)
}

func TestAggregatePrecedence(t *testing.T) {
	// One confirmed violation (persistence Error, immediate) + one Unknown (metrics disabled):
	// Error dominates -> NonCompliant, and the Unknown finding is still surfaced.
	ns := newNamespace(t)
	createDeployment(t, ns, "wl", deployOpts{volumes: []corev1.Volume{
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}})
	createService(t, ns, "svc", port)
	createEndpointSlice(t, ns, "svc", port, 1)
	c := buildContract(contractSpec{
		name:        "app",
		persistence: "persistent",
		ifaces:      []ifaceSpec{{name: "api"}},
		caps:        []capSpec{{typ: "metrics", iface: "api", path: "/metrics"}},
	})
	createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: c},
		Target: pactov1alpha1.TargetRef{
			ServiceName:       "svc",
			WorkloadRef:       &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"},
			InterfaceBindings: apiBinding("api"),
		},
	})
	p := reconcile(t, "p", ns, reconcileOpts{metricsEnabled: false}).pacto
	requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
	requireFinding(t, p, "PERSISTENCE_MISMATCH")    // the confirmed Error
	requireFinding(t, p, "OBSERVATION_UNSUPPORTED") // the coexisting Unknown, still surfaced
	if p.Status.Summary == nil || p.Status.Summary.ErrorCount == 0 || p.Status.Summary.UnknownCount == 0 {
		t.Fatalf("expected both error and unknown counts, got %+v", p.Status.Summary)
	}
}

// stubLoader returns a fixed load result or error, driving classifyLoadError through the real pipeline.
type stubLoader struct {
	res *loader.LoadResult
	err error
}

func (s stubLoader) Load(_ context.Context, _, _ string, _ *authn.AuthConfig) (*loader.LoadResult, error) {
	return s.res, s.err
}
func (s stubLoader) ListTags(_ context.Context, _ string, _ *authn.AuthConfig) ([]string, error) {
	return nil, nil
}

func TestLoadFailures(t *testing.T) {
	t.Run("malformed_contract_invalid", func(t *testing.T) {
		ns := newNamespace(t)
		// Unsupported pactoVersion -> structural failure -> Invalid, never reaches runtime aggregation.
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "pactoVersion: \"1.0\"\nservice:\n  name: app\n  version: 1.0.0\n"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		p := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusInvalid)
	})

	t.Run("transient_registry_unreachable_unknown", func(t *testing.T) {
		ns := newNamespace(t)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "oci://ghcr.io/e2e/" + ns + "-svc:1.0.0"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc"},
		})
		ldr := stubLoader{err: &oci.RegistryUnreachableError{Ref: "ghcr.io/e2e/svc:1.0.0", Err: fmt.Errorf("dial tcp: connection refused")}}
		p := reconcile(t, "p", ns, reconcileOpts{loader: ldr}).pacto
		// A transient obtain-failure is Unknown, NOT Invalid and NOT NonCompliant.
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
	})
}

// =====================================================================================
// dashboard consumption: per-service mapping (source_k8s -> ComputeCompliance)
// =====================================================================================

func TestDashboardPerServiceMapping(t *testing.T) {
	// Reconcile real CRs of each evaluated state, then feed them through the REAL dashboard k8s source
	// (all-namespaces mode, so lookup is by contract service name).
	compliant := reconcileWorkloadCompliant(t)
	unknown := reconcileExtensionUnknown(t)
	nonCompliant := reconcilePersistenceNonCompliant(t)
	invalid := reconcileInvalid(t)
	reference := reconcileReference(t)

	cases := []struct {
		name string
		cr   *pactov1alpha1.Pacto
		want dashboard.ComplianceStatus
	}{
		{"compliant_to_OK", compliant, dashboard.ComplianceOK},
		{"unknown_to_UNKNOWN", unknown, dashboard.ComplianceUnknown},
		{"noncompliant_to_ERROR", nonCompliant, dashboard.ComplianceError},
		{"invalid_to_ERROR", invalid, dashboard.ComplianceError},
		{"reference_to_REFERENCE", reference, dashboard.ComplianceReference},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := dashboard.NewK8sSource(newDashClient(t, tc.cr), "", "pactos")
			// Invalid short-circuits with a nil status.contract; the dashboard resolves such a CR by its
			// metadata.name, so look it up by CR name and exercise that real fallback. Every other case
			// carries a contract and is looked up by its service name.
			lookup := tc.cr.Name
			if tc.cr.Status.Contract != nil {
				lookup = tc.cr.Status.Contract.ServiceName
			}
			d, err := src.GetService(testCtx, lookup)
			if err != nil {
				t.Fatalf("dashboard GetService: %v", err)
			}
			if d.Compliance == nil || d.Compliance.Status != tc.want {
				t.Fatalf("dashboard compliance = %+v, want status %q (contractStatus %q)",
					d.Compliance, tc.want, d.ContractStatus)
			}
		})
	}
}

func reconcileWorkloadCompliant(t *testing.T) *pactov1alpha1.Pacto {
	ns := newNamespace(t)
	createDeployment(t, ns, "wl", deployOpts{})
	createPacto(t, "cmp", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "svc-compliant", workload: "service"})},
		Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"}},
	})
	return reconcile(t, "cmp", ns, reconcileOpts{}).pacto
}

func reconcileExtensionUnknown(t *testing.T) *pactov1alpha1.Pacto {
	ns := newNamespace(t)
	createPacto(t, "unk", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
			name: "svc-unknown", caps: []capSpec{{typ: "extension", ref: "example.com/x"}}})},
		Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
	})
	return reconcile(t, "unk", ns, reconcileOpts{}).pacto
}

func reconcilePersistenceNonCompliant(t *testing.T) *pactov1alpha1.Pacto {
	ns := newNamespace(t)
	createDeployment(t, ns, "wl", deployOpts{volumes: []corev1.Volume{
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}})
	createPacto(t, "ncp", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "svc-noncompliant", persistence: "persistent"})},
		Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "wl", Kind: "Deployment"}},
	})
	return reconcile(t, "ncp", ns, reconcileOpts{}).pacto
}

func reconcileInvalid(t *testing.T) *pactov1alpha1.Pacto {
	ns := newNamespace(t)
	createPacto(t, "inv", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{
			name: "svc-invalid", caps: []capSpec{{typ: "health", iface: "ghost", path: "/healthz"}}})},
		Target: pactov1alpha1.TargetRef{ServiceName: "svc"},
	})
	// Invalid short-circuits before populateContractStatus, so status.contract stays nil. Do NOT hand-patch
	// it: TestDashboardPerServiceMapping looks this case up by CR name so the dashboard's REAL nil-Contract
	// metadata.name fallback (getPacto: r.Metadata.Name == name) is exercised end to end.
	return reconcile(t, "inv", ns, reconcileOpts{}).pacto
}

func reconcileReference(t *testing.T) *pactov1alpha1.Pacto {
	ns := newNamespace(t)
	createPacto(t, "ref", ns, pactov1alpha1.PactoSpec{
		ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "svc-reference"})},
		// no target -> reference-only
	})
	return reconcile(t, "ref", ns, reconcileOpts{}).pacto
}

// =====================================================================================
// dashboard fleet math (B-2)
// =====================================================================================

// SOURCE OF TRUTH: the authoritative B-2 denominator guard lives in the engine frontend at
// pkg/dashboard/frontend/src/lib/format.test.ts (exercising aggregateByOwner's compliancePercent) — that
// vitest owns the fleet-% reducer. This e2e does NOT re-derive that logic as a contract; its job is to prove
// the OPERATOR emits the raw per-status counts that FEED the reducer, by driving the REAL source_k8s status
// mapping (serviceFromK8sStatus -> NormalizeContractStatus) over a fleet. fleetPercent below is only a local
// restatement of the section 1.5 denominator so those per-status counts can be asserted end to end; treat
// format.test.ts, not this helper, as the definition of the numbers.

func syntheticPacto(name, status string) *pactov1alpha1.Pacto {
	return &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "fleet"},
		Status: pactov1alpha1.PactoStatus{
			ContractStatus: status,
			Contract:       &pactov1alpha1.ContractInfo{ServiceName: name, Version: "1.0.0"},
		},
	}
}

// fleetPercent applies the section 1.5 denominator over dashboard-mapped statuses:
//
//	denominator = Compliant + NonCompliant + Warning + Unknown + Invalid (Reference/NotEvaluated excluded)
//	numerator   = Compliant
func fleetPercent(services []dashboard.Service) (pct float64, assessed, needsAttention, unknown int) {
	var compliant, nonCompliant, warning, invalid int
	for _, s := range services {
		switch s.ContractStatus {
		case dashboard.StatusCompliant:
			compliant++
		case dashboard.StatusNonCompliant:
			nonCompliant++
		case dashboard.StatusWarning:
			warning++
		case dashboard.StatusUnknown:
			unknown++
		case dashboard.StatusInvalid:
			invalid++
			// StatusReference and StatusNotEvaluated are excluded from the denominator.
		}
	}
	assessed = compliant + nonCompliant + warning + unknown + invalid
	needsAttention = nonCompliant + warning + invalid // Unknown is NOT needsAttention
	if assessed == 0 {
		return -1, 0, needsAttention, unknown // N/A sentinel
	}
	return float64(compliant) / float64(assessed) * 100, assessed, needsAttention, unknown
}

func fleetServices(t *testing.T, pactos ...*pactov1alpha1.Pacto) []dashboard.Service {
	t.Helper()
	src := dashboard.NewK8sSource(newDashClient(t, pactos...), "fleet", "pactos")
	services, err := src.ListServices(testCtx)
	if err != nil {
		t.Fatalf("dashboard ListServices: %v", err)
	}
	return services
}

func TestDashboardFleetMath(t *testing.T) {
	t.Run("one_compliant_99_unknown_is_1_percent", func(t *testing.T) {
		var fleet []*pactov1alpha1.Pacto
		fleet = append(fleet, syntheticPacto("svc-0", pactov1alpha1.ContractStatusCompliant))
		for i := 1; i < 100; i++ {
			fleet = append(fleet, syntheticPacto(fmt.Sprintf("svc-%d", i), pactov1alpha1.ContractStatusUnknown))
		}
		pct, assessed, needsAttention, unknown := fleetPercent(fleetServices(t, fleet...))
		if pct != 1 {
			t.Fatalf("compliancePercent = %v, want 1 (NOT 100)", pct)
		}
		if assessed != 100 || unknown != 99 {
			t.Fatalf("assessed=%d unknown=%d, want 100/99", assessed, unknown)
		}
		if needsAttention != 0 {
			t.Fatalf("needsAttention = %d, want 0 (Unknown is not needsAttention)", needsAttention)
		}
	})

	t.Run("all_unknown_is_0_percent_not_NA", func(t *testing.T) {
		var fleet []*pactov1alpha1.Pacto
		for i := 0; i < 10; i++ {
			fleet = append(fleet, syntheticPacto(fmt.Sprintf("svc-%d", i), pactov1alpha1.ContractStatusUnknown))
		}
		pct, assessed, _, _ := fleetPercent(fleetServices(t, fleet...))
		if pct != 0 {
			t.Fatalf("all-Unknown compliancePercent = %v, want 0 (NOT N/A)", pct)
		}
		if assessed != 10 {
			t.Fatalf("assessed = %d, want 10", assessed)
		}
	})

	t.Run("reference_and_notEvaluated_excluded", func(t *testing.T) {
		fleet := []*pactov1alpha1.Pacto{
			syntheticPacto("c", pactov1alpha1.ContractStatusCompliant),
			syntheticPacto("r", pactov1alpha1.ContractStatusReference),
			syntheticPacto("n", pactov1alpha1.ContractStatusNotEvaluated),
		}
		pct, assessed, _, _ := fleetPercent(fleetServices(t, fleet...))
		if assessed != 1 || pct != 100 {
			t.Fatalf("assessed=%d pct=%v, want 1/100 (Reference+NotEvaluated excluded)", assessed, pct)
		}
	})

	t.Run("invalid_counts_in_denominator_and_needsAttention", func(t *testing.T) {
		fleet := []*pactov1alpha1.Pacto{
			syntheticPacto("c", pactov1alpha1.ContractStatusCompliant),
			syntheticPacto("i", pactov1alpha1.ContractStatusInvalid),
		}
		pct, assessed, needsAttention, _ := fleetPercent(fleetServices(t, fleet...))
		if assessed != 2 || pct != 50 || needsAttention != 1 {
			t.Fatalf("assessed=%d pct=%v needsAttention=%d, want 2/50/1", assessed, pct, needsAttention)
		}
	})

	t.Run("secondary_conclusive_metric_distinguishes_failure_from_uncertainty", func(t *testing.T) {
		fleet := []*pactov1alpha1.Pacto{
			syntheticPacto("c", pactov1alpha1.ContractStatusCompliant),
			syntheticPacto("nc", pactov1alpha1.ContractStatusNonCompliant),
			syntheticPacto("w", pactov1alpha1.ContractStatusWarning),
			syntheticPacto("u1", pactov1alpha1.ContractStatusUnknown),
			syntheticPacto("u2", pactov1alpha1.ContractStatusUnknown),
		}
		services := fleetServices(t, fleet...)
		pct, assessed, _, unknown := fleetPercent(services)
		if assessed != 5 || pct != 20 || unknown != 2 {
			t.Fatalf("assessed=%d pct=%v unknown=%d, want 5/20/2", assessed, pct, unknown)
		}
		// runtimeEvaluated = compliant+warning+nonCompliant+unknown; conclusive = compliant+warning+nonCompliant.
		var compliant, warning, nonCompliant int
		for _, s := range services {
			switch s.ContractStatus {
			case dashboard.StatusCompliant:
				compliant++
			case dashboard.StatusWarning:
				warning++
			case dashboard.StatusNonCompliant:
				nonCompliant++
			}
		}
		runtimeEvaluated := compliant + warning + nonCompliant + unknown
		conclusive := compliant + warning + nonCompliant
		if runtimeEvaluated != 5 || conclusive != 3 {
			t.Fatalf("runtimeEvaluated=%d conclusive=%d, want 5/3", runtimeEvaluated, conclusive)
		}
	})

	t.Run("unknown_service_is_not_a_pass_per_service", func(t *testing.T) {
		u := syntheticPacto("only-unknown", pactov1alpha1.ContractStatusUnknown)
		src := dashboard.NewK8sSource(newDashClient(t, u), "fleet", "pactos")
		d, err := src.GetService(testCtx, "only-unknown")
		if err != nil {
			t.Fatalf("GetService: %v", err)
		}
		if d.Compliance == nil || d.Compliance.Status != dashboard.ComplianceUnknown {
			t.Fatalf("Unknown service mapped to %+v, want ComplianceUnknown (must not read as a pass)", d.Compliance)
		}
	})
}
