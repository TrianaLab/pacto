/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
)

// TestObservationSource_Validate is the configuration counterexample set. Each
// case is something a declarative configuration could plausibly say and that must
// be refused outright rather than normalized into something that happens to work.
func TestObservationSource_Validate(t *testing.T) {
	valid := ObservationSource{Name: "orders", File: "traces.json", ExistingClaim: "orders-traces"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		src  ObservationSource
		want string
	}{
		{"no name", ObservationSource{File: "t.json", ConfigMap: "cm"}, "name must be set"},
		{
			"name too long",
			ObservationSource{Name: strings.Repeat("a", maxObservationNameLength+1), File: "t.json", ConfigMap: "cm"},
			"longer than",
		},
		{"name not a label", ObservationSource{Name: "Orders_EU", File: "t.json", ConfigMap: "cm"}, "invalid observation source name"},
		{"no file", ObservationSource{Name: "orders", ConfigMap: "cm"}, "must set file"},
		{"absolute file", ObservationSource{Name: "orders", File: "/etc/passwd", ConfigMap: "cm"}, "relative path inside its mount"},
		{"escaping file", ObservationSource{Name: "orders", File: "../../etc/passwd", ConfigMap: "cm"}, "relative path inside its mount"},
		{"escaping mid-path", ObservationSource{Name: "orders", File: "a/../../b.json", ConfigMap: "cm"}, "relative path inside its mount"},
		{"whitespace in file", ObservationSource{Name: "orders", File: "my traces.json", ConfigMap: "cm"}, "must not contain whitespace"},
		{"nested file", ObservationSource{Name: "orders", File: "exports/traces.json", ConfigMap: "cm"}, "not a nested path"},
		{"comma in file", ObservationSource{Name: "orders", File: "trace,part.json", ConfigMap: "cm"}, "separates fields"},
		{
			"claim is not an object name",
			ObservationSource{Name: "orders", File: "t.json", ExistingClaim: "Orders_Trace_Export"},
			"not a valid Kubernetes object name",
		},
		{
			"configMap is not an object name",
			ObservationSource{Name: "orders", File: "t.json", ConfigMap: "orders traces"},
			"not a valid Kubernetes object name",
		},
		{
			"two backings",
			ObservationSource{Name: "orders", File: "t.json", ConfigMap: "cm", ExistingClaim: "pvc"},
			"exactly one backing is allowed",
		},
		{"no backing", ObservationSource{Name: "orders", File: "t.json"}, "exactly one of existingClaim or configMap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.src.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestObservationSource_Paths proves the mount layout is a function of the name
// alone, so two sources can carry identically named files without colliding.
func TestObservationSource_Paths(t *testing.T) {
	eu := ObservationSource{Name: "eu", File: "traces.json", ExistingClaim: "eu-traces"}
	us := ObservationSource{Name: "us", File: "traces.json", ExistingClaim: "us-traces"}
	if eu.FilePath() != "/var/lib/pacto/observation/eu/traces.json" {
		t.Errorf("eu FilePath = %q", eu.FilePath())
	}
	if eu.FilePath() == us.FilePath() {
		t.Errorf("same-basename sources share a path: %q", eu.FilePath())
	}
	if eu.VolumeName() != "obs-eu" {
		t.Errorf("eu VolumeName = %q", eu.VolumeName())
	}
	// The dashboard is handed FilePath() and nothing else, and roots its read at
	// that path's parent. If the parent were ever anything but the declared mount,
	// the read root would sit below the mount and a symlink in the volume would
	// have a directory to escape through.
	if got := path.Dir(eu.FilePath()); got != eu.MountPath() {
		t.Errorf("the file's parent is %q, but the declared mount is %q", got, eu.MountPath())
	}
}

// TestConfig_ValidateObservation_DuplicateName rejects the one collision the
// naming rules cannot design away: the same name declared twice. Repairing it
// would mean two Data Sources sharing one identity and one volume.
func TestConfig_ValidateObservation_DuplicateName(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "ns", Observation: []ObservationSource{
		{Name: "orders", File: "a.json", ExistingClaim: "pvc-a"},
		{Name: "orders", File: "b.json", ExistingClaim: "pvc-b"},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `duplicate observation source name "orders"`) {
		t.Fatalf("Validate() = %v, want a duplicate-name error", err)
	}
}

// TestConfig_ValidateObservation_InvalidSource proves a bad source fails the whole
// dashboard configuration at operator startup, not at the first reconcile.
func TestConfig_ValidateObservation_InvalidSource(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "ns", Observation: []ObservationSource{
		{Name: "orders", File: "../escape.json", ExistingClaim: "pvc"},
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected a path-escape error from Config.Validate")
	}
}

// TestConfig_ObservationEnv covers the dashboard wire: sorted NAME=PATH pairs, and
// nothing at all when no source is configured.
func TestConfig_ObservationEnv(t *testing.T) {
	cfg := Config{Observation: []ObservationSource{
		{Name: "orders", File: "traces.json", ExistingClaim: "orders-traces"},
		{Name: "checkout", File: "checkout-traces.json", ConfigMap: "checkout-traces"},
	}}
	want := "checkout=/var/lib/pacto/observation/checkout/checkout-traces.json " +
		"orders=/var/lib/pacto/observation/orders/traces.json"
	if got := cfg.ObservationEnv(); got != want {
		t.Errorf("ObservationEnv() = %q, want %q", got, want)
	}
	if got := (Config{}).ObservationEnv(); got != "" {
		t.Errorf("ObservationEnv() with no sources = %q, want empty", got)
	}
}

func TestParseObservationSource(t *testing.T) {
	got, err := ParseObservationSource("name=orders,file=traces.json,existingClaim=orders-traces")
	if err != nil {
		t.Fatalf("ParseObservationSource: %v", err)
	}
	want := ObservationSource{Name: "orders", File: "traces.json", ExistingClaim: "orders-traces"}
	if got != want {
		t.Errorf("parsed = %+v, want %+v", got, want)
	}
	if cm, err := ParseObservationSource("name=fixture,file=t.json,configMap=traces"); err != nil ||
		cm.ConfigMap != "traces" {
		t.Errorf("configMap backing = %+v, %v", cm, err)
	}
	// Only the first "=" of a field separates key from value, so a file name may
	// carry one. That is the only lexical freedom the flat wire keeps.
	if eq, err := ParseObservationSource("name=orders,file=trace=export.json,configMap=cm"); err != nil ||
		eq.File != "trace=export.json" {
		t.Errorf("file with an equals sign = %+v, %v", eq, err)
	}
	for _, spec := range []string{
		"name=orders,file",                              // not key=value
		"name=orders,file=t.json,claim=pvc",             // unknown field
		"name=orders,file=t.json",                       // no backing (Validate)
		"name=Orders,file=t.json,configMap=cm",          // not a label (Validate)
		"name=orders,file=/abs,configMap=cm",            // escaping path (Validate)
		"name=orders,file=t.json,configMap=cm,",         // trailing separator
		"name=orders,file=trace,part.json,configMap=cm", // the comma counterexample: "part.json" is not key=value
	} {
		if _, err := ParseObservationSource(spec); err == nil {
			t.Errorf("ParseObservationSource(%q) = nil error, want a rejection", spec)
		}
	}
}

// podTemplateJSON renders a deployment apply configuration for comparison.
func podTemplateJSON(t *testing.T, cfg Config) string {
	t.Helper()
	deploy, ok := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration")
	}
	data, err := json.Marshal(deploy.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDeploymentAC_WithObservationSources is the deployment acceptance: each
// configured source becomes exactly one read-only mount over its declared backing,
// the dashboard is told about it by name, and the evidence and OCI-credential
// wiring is untouched.
func TestDeploymentAC_WithObservationSources(t *testing.T) {
	cfg := Config{
		Enabled:           true,
		Image:             "img:v1",
		Namespace:         "test-ns",
		OCISecret:         "my-creds",
		EvidenceSourceURL: "http://pacto-evidence.test-ns.svc:8686",
		Observation: []ObservationSource{
			{Name: "orders", File: "traces.json", ExistingClaim: "orders-traces"},
			{Name: "fixture", File: "traces.json", ConfigMap: "fixture-traces"},
		},
	}
	deploy, ok := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration")
	}
	spec := deploy.Spec.Template.Spec
	container := spec.Containers[0]

	mounts := map[string]string{}
	for _, vm := range container.VolumeMounts {
		if vm.ReadOnly == nil || !*vm.ReadOnly {
			if strings.HasPrefix(*vm.Name, "obs-") {
				t.Errorf("observation mount %q is not read-only", *vm.Name)
			}
			continue
		}
		mounts[*vm.Name] = *vm.MountPath
	}
	if mounts["obs-orders"] != "/var/lib/pacto/observation/orders" {
		t.Errorf("obs-orders mount = %q", mounts["obs-orders"])
	}
	if mounts["obs-fixture"] != "/var/lib/pacto/observation/fixture" {
		t.Errorf("obs-fixture mount = %q", mounts["obs-fixture"])
	}
	if _, ok := mounts["oci-creds"]; !ok {
		t.Error("observation wiring displaced the oci-creds mount")
	}

	var claim, configMap bool
	for _, v := range spec.Volumes {
		switch *v.Name {
		case "obs-orders":
			if v.PersistentVolumeClaim == nil || *v.PersistentVolumeClaim.ClaimName != "orders-traces" {
				t.Errorf("obs-orders volume = %+v, want the declared PVC", v)
				continue
			}
			if v.PersistentVolumeClaim.ReadOnly == nil || !*v.PersistentVolumeClaim.ReadOnly {
				t.Error("obs-orders PVC volume must be read-only: Pacto never writes to a trace export")
			}
			claim = true
		case "obs-fixture":
			if v.ConfigMap == nil || *v.ConfigMap.Name != "fixture-traces" {
				t.Errorf("obs-fixture volume = %+v, want the declared ConfigMap", v)
				continue
			}
			// Optional, so an absent ConfigMap degrades the Data Source instead of
			// holding the pod in ContainerCreating.
			if v.ConfigMap.Optional == nil || !*v.ConfigMap.Optional {
				t.Error("obs-fixture ConfigMap volume must be optional")
			}
			configMap = true
		}
	}
	if !claim || !configMap {
		t.Errorf("expected both observation volumes, got %+v", spec.Volumes)
	}

	env := map[string]string{}
	for _, e := range container.Env {
		env[*e.Name] = *e.Value
	}
	want := "fixture=/var/lib/pacto/observation/fixture/traces.json " +
		"orders=/var/lib/pacto/observation/orders/traces.json"
	if env[ObservationEnvVar] != want {
		t.Errorf("%s = %q, want %q", ObservationEnvVar, env[ObservationEnvVar], want)
	}
	if env["PACTO_EVIDENCE_SOURCE_URL"] != cfg.EvidenceSourceURL {
		t.Error("observation wiring displaced the evidence source URL")
	}
}

// TestDeploymentAC_ObservationOrderIsNotIdentity proves reordering the declared
// sources produces a byte-identical pod template. Order is presentation; identity
// is the name. A rollout must mean the configuration changed, not that someone
// resorted a YAML list.
func TestDeploymentAC_ObservationOrderIsNotIdentity(t *testing.T) {
	a := ObservationSource{Name: "orders", File: "traces.json", ExistingClaim: "orders-traces"}
	b := ObservationSource{Name: "checkout", File: "traces.json", ConfigMap: "checkout-traces"}
	base := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}

	forward, reversed := base, base
	forward.Observation = []ObservationSource{a, b}
	reversed.Observation = []ObservationSource{b, a}
	if podTemplateJSON(t, forward) != podTemplateJSON(t, reversed) {
		t.Errorf("reordering changed the pod template:\n%s\n%s", podTemplateJSON(t, forward), podTemplateJSON(t, reversed))
	}
}

// TestDeploymentAC_WithoutObservation proves removal is complete: no managed
// volume, no mount, no environment variable left behind.
func TestDeploymentAC_WithoutObservation(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	deploy, ok := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration")
	}
	spec := deploy.Spec.Template.Spec
	for _, v := range spec.Volumes {
		if strings.HasPrefix(*v.Name, "obs-") {
			t.Errorf("orphaned observation volume %q", *v.Name)
		}
	}
	for _, vm := range spec.Containers[0].VolumeMounts {
		if strings.HasPrefix(*vm.Name, "obs-") {
			t.Errorf("orphaned observation mount %q", *vm.Name)
		}
	}
	for _, e := range spec.Containers[0].Env {
		if *e.Name == ObservationEnvVar {
			t.Errorf("orphaned %s env var", ObservationEnvVar)
		}
	}
}

// TestDeploymentAC_ObservationChangeRollsTheDeployment proves configuration
// reaches the pod template: changing a source's file changes the rendered
// template, so Kubernetes rolls the dashboard.
func TestDeploymentAC_ObservationChangeRollsTheDeployment(t *testing.T) {
	base := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	before, after := base, base
	before.Observation = []ObservationSource{{Name: "orders", File: "traces.json", ExistingClaim: "pvc"}}
	after.Observation = []ObservationSource{{Name: "orders", File: "traces-v2.json", ExistingClaim: "pvc"}}
	if podTemplateJSON(t, before) == podTemplateJSON(t, after) {
		t.Error("changing the configured file left the pod template unchanged; the dashboard would never roll")
	}
}
