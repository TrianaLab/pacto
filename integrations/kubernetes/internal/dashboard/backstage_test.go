/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"testing"

	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func envOf(cfg Config) map[string]corev1ac.EnvVarApplyConfiguration {
	env := map[string]corev1ac.EnvVarApplyConfiguration{}
	for _, e := range deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec.Containers[0].Env {
		env[*e.Name] = e
	}
	return env
}

// TestDashboardBackstageEnv: the catalog URL reaches the dashboard only when set.
func TestDashboardBackstageEnv(t *testing.T) {
	env := envOf(Config{Image: "i", Namespace: "n", BackstageToken: KeyRef{Name: "backstage-reader"}})
	for _, name := range []string{"PACTO_DASHBOARD_BACKSTAGE_URL", "PACTO_BACKSTAGE_TOKEN"} {
		if _, ok := env[name]; ok {
			t.Errorf("no URL configured, yet %s is set", name)
		}
	}
	env = envOf(Config{Image: "i", Namespace: "n", BackstageURL: "https://backstage.example.com"})
	if e, ok := env["PACTO_DASHBOARD_BACKSTAGE_URL"]; !ok || *e.Value != "https://backstage.example.com" {
		t.Errorf("PACTO_DASHBOARD_BACKSTAGE_URL = %+v", e)
	}
	if _, ok := env["PACTO_BACKSTAGE_TOKEN"]; ok {
		t.Error("no token Secret named, yet PACTO_BACKSTAGE_TOKEN is set")
	}
}

// TestDashboardBackstageTokenIsSecretKeyRef: the reader token reaches the
// dashboard only as a secretKeyRef the kubelet resolves (default key token),
// never as a literal the operator read.
func TestDashboardBackstageTokenIsSecretKeyRef(t *testing.T) {
	cfg := Config{Image: "i", Namespace: "n", BackstageURL: "https://b", BackstageToken: KeyRef{Name: "backstage-reader"}}
	e := envOf(cfg)["PACTO_BACKSTAGE_TOKEN"]
	if e.Value != nil || e.ValueFrom == nil || *e.ValueFrom.SecretKeyRef.Name != "backstage-reader" || *e.ValueFrom.SecretKeyRef.Key != "token" ||
		e.ValueFrom.SecretKeyRef.Optional == nil || *e.ValueFrom.SecretKeyRef.Optional {
		t.Fatalf("the token goes by a required secretKeyRef only: %+v", e)
	}
	cfg.BackstageToken.Key = "reader"
	if e := envOf(cfg)["PACTO_BACKSTAGE_TOKEN"]; *e.ValueFrom.SecretKeyRef.Key != "reader" {
		t.Errorf("a custom key is used: %+v", e)
	}
}
