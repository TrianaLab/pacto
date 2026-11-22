/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
)

func testOwnerRef() *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithAPIVersion("apps/v1").
		WithKind("Deployment").
		WithName("pacto-operator").
		WithUID(types.UID("test-uid-1234"))
}

func TestServiceAccountAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := serviceAccountAC(cfg)
	sa, ok := ac.(*corev1ac.ServiceAccountApplyConfiguration)
	if !ok {
		t.Fatalf("expected *ServiceAccountApplyConfiguration, got %T", ac)
	}
	if len(sa.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(sa.OwnerReferences))
	}
	if *sa.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *sa.OwnerReferences[0].Name)
	}
}

func TestDeploymentAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	if len(deploy.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(deploy.OwnerReferences))
	}
	if *deploy.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *deploy.OwnerReferences[0].Name)
	}
}

func TestDeploymentAC_Auth(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "ns", MiraURL: "http://mira:8080",
		Auth: AuthConfig{IssuerURL: "https://idp", ClientID: "pacto-dashboard", RedirectURL: "https://dash/auth/callback",
			ExtraScopes: []string{"a", "b"}, ClientSecretName: "oidc", UsernameClaim: "email", UsernamePrefix: "-",
			GroupsClaim: "roles", GroupsPrefix: "oidc:", SessionIdleTimeout: 30 * time.Minute, SessionMaxAge: 8 * time.Hour, TokenLogin: true},
		DeliveryNamespace: "delivery"}
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	// Sessions live in one process's memory, so a signed-in dashboard must stay
	// one replica: a second replica would sign people out at random.
	if *deploy.Spec.Replicas != 1 {
		t.Fatalf("sign-in sessions need one replica, got %d", *deploy.Spec.Replicas)
	}
	env := map[string]corev1ac.EnvVarApplyConfiguration{}
	for _, e := range deploy.Spec.Template.Spec.Containers[0].Env {
		env[*e.Name] = e
	}
	for name, want := range map[string]string{"PACTO_DASHBOARD_OIDC_ISSUER_URL": "https://idp", "PACTO_DASHBOARD_OIDC_CLIENT_ID": "pacto-dashboard",
		"PACTO_DASHBOARD_OIDC_REDIRECT_URL": "https://dash/auth/callback", "PACTO_DASHBOARD_OIDC_EXTRA_SCOPES": "a b",
		"PACTO_DASHBOARD_MIRA_URL": "http://mira:8080", "PACTO_DASHBOARD_DELIVERY_NAMESPACE": "delivery",
		"PACTO_DASHBOARD_OIDC_USERNAME_CLAIM": "email", "PACTO_DASHBOARD_OIDC_USERNAME_PREFIX": "-", "PACTO_DASHBOARD_OIDC_GROUPS_CLAIM": "roles",
		"PACTO_DASHBOARD_OIDC_GROUPS_PREFIX": "oidc:", "PACTO_DASHBOARD_SESSION_IDLE_TIMEOUT": "30m0s", "PACTO_DASHBOARD_SESSION_MAX_AGE": "8h0m0s",
		"PACTO_DASHBOARD_TOKEN_LOGIN": "true"} {
		if e, ok := env[name]; !ok || *e.Value != want {
			t.Errorf("%s: want %q, got %+v", name, want, e)
		}
	}
	secret := env["PACTO_DASHBOARD_OIDC_CLIENT_SECRET"]
	if secret.Value != nil || *secret.ValueFrom.SecretKeyRef.Name != "oidc" || *secret.ValueFrom.SecretKeyRef.Key != "client-secret" {
		t.Fatalf("the client secret goes by secretKeyRef only: %+v", secret)
	}
	cfg.Auth.ClientSecretKey = "secret"
	for _, e := range deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec.Containers[0].Env {
		if *e.Name == "PACTO_DASHBOARD_OIDC_CLIENT_SECRET" && *e.ValueFrom.SecretKeyRef.Key != "secret" {
			t.Fatal("a custom key is used")
		}
	}
	for _, e := range deploymentAC(Config{Image: "i", Namespace: "n"}).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec.Containers[0].Env {
		if strings.Contains(*e.Name, "OIDC") || strings.Contains(*e.Name, "MIRA") || strings.Contains(*e.Name, "SESSION") || strings.Contains(*e.Name, "TOKEN") {
			t.Errorf("no sign-in configured, no %s", *e.Name)
		}
	}
}

// TestDeploymentAC_AuthVolumes: the issuer CA bundle and the delivery trust
// config mount read-only, only the key the dashboard reads, and the dashboard
// is told the file.
func TestDeploymentAC_AuthVolumes(t *testing.T) {
	spec := func(cfg Config) (map[string]string, map[string]corev1ac.VolumeApplyConfiguration) {
		pod := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec
		env := map[string]string{}
		for _, e := range pod.Containers[0].Env {
			if e.Value != nil {
				env[*e.Name] = *e.Value
			}
		}
		vols := map[string]corev1ac.VolumeApplyConfiguration{}
		for _, v := range pod.Volumes {
			vols[*v.Name] = v
		}
		for _, m := range pod.Containers[0].VolumeMounts {
			if _, ok := vols[*m.Name]; !ok || (*m.Name != "cache" && !*m.ReadOnly) {
				t.Errorf("mount %s: no volume or writable", *m.Name)
			}
		}
		return env, vols
	}
	cfg := Config{Image: "i", Namespace: "n", Auth: AuthConfig{IssuerURL: "https://idp", InternalIssuerURL: "http://idp.idp:5556",
		DisplayName: "Keycloak", CAConfigMap: "corp-ca"}, DeliveryTrust: KeyRef{Name: "trust"}}
	env, vols := spec(cfg)
	for name, want := range map[string]string{"PACTO_DASHBOARD_OIDC_INTERNAL_ISSUER_URL": "http://idp.idp:5556", "PACTO_DASHBOARD_OIDC_DISPLAY_NAME": "Keycloak",
		"PACTO_DASHBOARD_OIDC_CA_FILE": "/etc/pacto/oidc-ca/ca.crt", "PACTO_DASHBOARD_DELIVERY_TRUST": "/etc/pacto/delivery-trust/trust.yaml"} {
		if env[name] != want {
			t.Errorf("%s: want %q, got %q", name, want, env[name])
		}
	}
	if ca := vols["oidc-ca"].ConfigMap; ca == nil || *ca.Name != "corp-ca" || *ca.Items[0].Key != "ca.crt" {
		t.Errorf("the CA bundle comes from the ConfigMap: %+v", vols["oidc-ca"])
	}
	if tr := vols["delivery-trust"].ConfigMap; tr == nil || *tr.Name != "trust" || *tr.Items[0].Key != "trust.yaml" {
		t.Errorf("the trust config comes from its ConfigMap: %+v", vols["delivery-trust"])
	}

	cfg.Auth = AuthConfig{IssuerURL: "https://idp", CASecret: "corp-ca", CAKey: "bundle.pem"}
	cfg.DeliveryTrust.Key = "recorder.pem"
	env, vols = spec(cfg)
	if ca := vols["oidc-ca"].Secret; ca == nil || *ca.SecretName != "corp-ca" || *ca.Items[0].Key != "bundle.pem" ||
		env["PACTO_DASHBOARD_OIDC_CA_FILE"] != "/etc/pacto/oidc-ca/bundle.pem" {
		t.Errorf("the CA bundle comes from the Secret's key: %+v %v", vols["oidc-ca"], env)
	}
	if env["PACTO_DASHBOARD_DELIVERY_TRUST"] != "/etc/pacto/delivery-trust/recorder.pem" {
		t.Errorf("a custom trust key: %v", env)
	}

	env, vols = spec(Config{Image: "i", Namespace: "n"})
	if _, ok := vols["oidc-ca"]; ok || env["PACTO_DASHBOARD_DELIVERY_TRUST"] != "" || vols["delivery-trust"].Name != nil {
		t.Errorf("nothing configured, nothing mounted: %v %v", env, vols)
	}
}

// TestDashboardRoleReadsDeliveryOnly: every delivery action passes the
// person's token through, so the dashboard's own role only reads Stages and
// Operations, into its shared cache, and never impersonates. Its one write is
// asking the apiserver what a signed-in person may read; it lists namespaces
// only to know whom to ask.
func TestDashboardRoleReadsDeliveryOnly(t *testing.T) {
	role := clusterRoleAC(false).(*rbacv1ac.ClusterRoleApplyConfiguration)
	sar, reads := false, false
	for _, r := range role.Rules {
		if slices.Equal(r.APIGroups, []string{"authorization.k8s.io"}) && slices.Equal(r.Resources, []string{"subjectaccessreviews"}) &&
			slices.Equal(r.Verbs, []string{"create"}) {
			sar = true
			continue
		}
		if slices.Equal(r.APIGroups, []string{"delivery.pacto.trianalab.io"}) {
			reads = slices.Equal(r.Resources, []string{"stages", "operations"}) && slices.Equal(r.Verbs, []string{"get", "list", "watch"})
			if !reads {
				t.Errorf("the dashboard role reaches delivery beyond reading Stages and Operations: %v %v", r.Resources, r.Verbs)
			}
			continue
		}
		if slices.Equal(r.APIGroups, []string{""}) && slices.Equal(r.Resources, []string{"namespaces"}) && !slices.Equal(r.Verbs, []string{"list"}) &&
			(!slices.Equal(r.Verbs, []string{"get"}) || !slices.Equal(r.ResourceNames, []string{"kube-system"})) {
			t.Errorf("the dashboard role may %v namespaces %v", r.Verbs, r.ResourceNames)
		}
		for _, g := range r.APIGroups {
			if g == "*" {
				t.Errorf("the dashboard role reaches %q", g)
			}
		}
		for _, res := range r.Resources {
			if res == "pods" || res == "secrets" || res == "configmaps" {
				t.Errorf("the dashboard role reads %s", res)
			}
		}
		if slices.Equal(r.APIGroups, []string{"apps"}) || slices.Equal(r.APIGroups, []string{"batch"}) {
			if !slices.Equal(r.Verbs, []string{"list"}) {
				t.Errorf("the dashboard role may %v workloads %v; inferred services need list only", r.Verbs, r.Resources)
			}
		}
		for _, v := range r.Verbs {
			if v == "impersonate" || v == "*" || v == "create" || v == "update" || v == "patch" || v == "delete" {
				t.Errorf("the dashboard role may %s", v)
			}
		}
	}
	if !sar {
		t.Error("the dashboard role cannot ask which namespaces a person may read")
	}
	if !reads {
		t.Error("the dashboard role cannot fill its shared cache of Stages and Operations")
	}
}

// TestDashboardClusterRoleReadsClusterIdentity: the dashboard's live source
// names its cluster by the kube-system namespace UID, so the role reads that
// one namespace by name and keeps listing namespace names as before.
func TestDashboardClusterRoleReadsClusterIdentity(t *testing.T) {
	role := clusterRoleAC(false).(*rbacv1ac.ClusterRoleApplyConfiguration)
	var gets, lists int
	for _, r := range role.Rules {
		if !slices.Equal(r.APIGroups, []string{""}) || !slices.Equal(r.Resources, []string{"namespaces"}) {
			continue
		}
		switch {
		case slices.Equal(r.Verbs, []string{"get"}) && slices.Equal(r.ResourceNames, []string{"kube-system"}):
			gets++
		case slices.Equal(r.Verbs, []string{"list"}) && len(r.ResourceNames) == 0:
			lists++
		default:
			t.Errorf("unexpected namespaces rule: verbs %v names %v", r.Verbs, r.ResourceNames)
		}
	}
	if gets != 1 || lists != 1 {
		t.Errorf("want one kube-system get rule and one list rule, got %d and %d", gets, lists)
	}
}

func TestDeploymentAC_WithOCISecrets(t *testing.T) {
	cfg := Config{
		Enabled:    true,
		Image:      "img:v1",
		Namespace:  "test-ns",
		OCISecrets: []string{"my-creds"},
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	container := deploy.Spec.Template.Spec.Containers[0]

	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if *vm.Name == "oci-creds" {
			foundMount = true
		}
	}
	if !foundMount {
		t.Error("expected oci-creds volume mount")
	}

	var foundVolume bool
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name == "oci-creds" {
			foundVolume = true
		}
	}
	if !foundVolume {
		t.Error("expected oci-creds volume")
	}
}

func TestDeploymentAC_WithEvidenceSourceURL(t *testing.T) {
	cfg := Config{
		Enabled:           true,
		Image:             "img:v1",
		Namespace:         "test-ns",
		EvidenceSourceURL: "http://pacto-evidence.test-ns.svc:8686",
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	var found bool
	for _, env := range container.Env {
		if *env.Name == "PACTO_EVIDENCE_SOURCE_URL" {
			found = true
			if *env.Value != cfg.EvidenceSourceURL {
				t.Errorf("expected PACTO_EVIDENCE_SOURCE_URL=%q, got %q", cfg.EvidenceSourceURL, *env.Value)
			}
		}
	}
	if !found {
		t.Error("expected PACTO_EVIDENCE_SOURCE_URL env var when EvidenceSourceURL is set")
	}
}

func TestDeploymentAC_WithoutEvidenceSourceURL(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	ac := deploymentAC(cfg)
	deploy := ac.(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]
	for _, env := range container.Env {
		if *env.Name == "PACTO_EVIDENCE_SOURCE_URL" {
			t.Error("unexpected PACTO_EVIDENCE_SOURCE_URL env var when EvidenceSourceURL is empty")
		}
	}
}

// The dashboard resolves contract revisions itself, so the plain-HTTP allowance
// has to reach the managed workload — the controller honouring it is not enough.
func TestDeploymentAC_InsecureRegistries(t *testing.T) {
	for _, tc := range []struct {
		name, configured, want string
	}{
		{"configured", "reg.pacto-system.svc:5000", "reg.pacto-system.svc:5000"},
		{"unset", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", InsecureRegistries: tc.configured}
			deploy, ok := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
			if !ok {
				t.Fatal("expected *DeploymentApplyConfiguration")
			}
			var got string
			for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
				if *env.Name == loader.InsecureRegistriesEnvVar {
					got = *env.Value
				}
			}
			if got != tc.want {
				t.Errorf("%s=%q, want %q", loader.InsecureRegistriesEnvVar, got, tc.want)
			}
		})
	}
}

// The dashboard takes PACTO_DASHBOARD_REPO entries raw (only its arguments get
// oci:// stripped), so the operator trims the scheme before joining.
func TestDeploymentRepositoriesEnv(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos []string
		want  string
		set   bool
	}{
		{"configured", []string{"oci://ghcr.io/acme/paths/http-service", "ghcr.io/acme/paths/worker"},
			"ghcr.io/acme/paths/http-service,ghcr.io/acme/paths/worker", true},
		{"unset", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", Repositories: tc.repos}
			deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
			var got string
			var set bool
			for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
				if *env.Name == "PACTO_DASHBOARD_REPO" {
					got, set = *env.Value, true
				}
			}
			if got != tc.want || set != tc.set {
				t.Errorf("PACTO_DASHBOARD_REPO=%q (set %v), want %q (set %v)", got, set, tc.want, tc.set)
			}
		})
	}
}

func externalCatalogEnv(cfg Config) (string, bool) {
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if *env.Name == "PACTO_DASHBOARD_EXTERNAL_CATALOG" {
			return *env.Value, true
		}
	}
	return "", false
}

// The dashboard strips oci:// from the catalog reference itself, so the
// operator passes it verbatim.
func TestApplySetsExternalCatalogEnv(t *testing.T) {
	ref := "oci://ghcr.io/acme/catalog@sha256:" + strings.Repeat("a", 64)
	got, set := externalCatalogEnv(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", ExternalCatalog: ref})
	if !set || got != ref {
		t.Errorf("PACTO_DASHBOARD_EXTERNAL_CATALOG=%q (set %v), want %q", got, set, ref)
	}
}

func TestApplyOmitsEmptyExternalCatalog(t *testing.T) {
	if got, set := externalCatalogEnv(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}); set {
		t.Errorf("PACTO_DASHBOARD_EXTERNAL_CATALOG=%q is set without a catalog", got)
	}
}

func TestServiceAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := serviceAC(cfg)
	svc, ok := ac.(*corev1ac.ServiceApplyConfiguration)
	if !ok {
		t.Fatalf("expected *ServiceApplyConfiguration, got %T", ac)
	}
	if len(svc.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(svc.OwnerReferences))
	}
	if *svc.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *svc.OwnerReferences[0].Name)
	}
}

func TestOCISecretAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := ociSecretAC(cfg, []byte(`{"auths":{}}`))
	secret, ok := ac.(*corev1ac.SecretApplyConfiguration)
	if !ok {
		t.Fatalf("expected *SecretApplyConfiguration, got %T", ac)
	}
	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(secret.OwnerReferences))
	}
	if *secret.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *secret.OwnerReferences[0].Name)
	}
}

// The anonymous choice reaches the dashboard only when nobody signs in: with
// sign-in configured it would change nothing, so it is left out.
func TestAllowAnonymousEnvOnlyWithoutSignIn(t *testing.T) {
	anonymous := func(a AuthConfig) bool {
		for _, e := range deploymentAC(Config{Image: "i", Namespace: "n", Auth: a}).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec.Containers[0].Env {
			if *e.Name == "PACTO_DASHBOARD_ALLOW_ANONYMOUS" {
				return *e.Value == "true"
			}
		}
		return false
	}
	for _, c := range []struct {
		name string
		auth AuthConfig
		want bool
	}{
		{"chosen without sign-in", AuthConfig{AllowAnonymous: true}, true},
		{"not chosen", AuthConfig{}, false},
		{"issuer", AuthConfig{AllowAnonymous: true, IssuerURL: "https://idp", ClientID: "c", RedirectURL: "https://d/cb"}, false},
		{"token sign-in", AuthConfig{AllowAnonymous: true, TokenLogin: true}, false},
	} {
		if got := anonymous(c.auth); got != c.want {
			t.Errorf("%s: PACTO_DASHBOARD_ALLOW_ANONYMOUS=%v, want %v", c.name, got, c.want)
		}
	}
}

func envValue(cfg Config, name string) (string, bool) {
	for _, e := range deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration).Spec.Template.Spec.Containers[0].Env {
		if *e.Name == name && e.Value != nil {
			return *e.Value, true
		}
	}
	return "", false
}

// TestApplySetsStatsRegistryEnv: the Metrics page reads the stats repository
// the operator names, through the env var `pacto dashboard` falls back to.
func TestApplySetsStatsRegistryEnv(t *testing.T) {
	cfg := Config{Image: "i", Namespace: "n", StatsRegistry: "ghcr.io/acme/pacto-stats"}
	if v, ok := envValue(cfg, "PACTO_STATS_REGISTRY"); !ok || v != "ghcr.io/acme/pacto-stats" {
		t.Fatalf("PACTO_STATS_REGISTRY: got %q, %v", v, ok)
	}
}

func TestApplyNoStatsRegistryNoEnv(t *testing.T) {
	if v, ok := envValue(Config{Image: "i", Namespace: "n"}, "PACTO_STATS_REGISTRY"); ok {
		t.Fatalf("no stats registry configured, yet PACTO_STATS_REGISTRY=%q", v)
	}
}

// The dashboard reads what the operator detected as a kubelet-mounted file, so
// its ServiceAccount gains no ConfigMap rule; an optional volume keeps the pod
// starting before the first detection.
func TestDeploymentAC_DetectedVolumeOptional(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", DetectedConfigMap: "pacto-operator-detected"}
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]

	var env bool
	for _, e := range container.Env {
		if *e.Name == detectedEnvVar {
			env = *e.Value == "/etc/pacto/detected/detected.json"
		}
	}
	if !env {
		t.Errorf("expected %s=/etc/pacto/detected/detected.json", detectedEnvVar)
	}
	var mount bool
	for _, vm := range container.VolumeMounts {
		if *vm.Name == "detected" {
			mount = *vm.MountPath == "/etc/pacto/detected" && vm.ReadOnly != nil && *vm.ReadOnly
		}
	}
	if !mount {
		t.Error("expected a read-only detected mount at /etc/pacto/detected")
	}
	var vol bool
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name != "detected" {
			continue
		}
		cm := v.ConfigMap
		vol = cm != nil && *cm.Name == "pacto-operator-detected" && cm.Optional != nil && *cm.Optional &&
			len(cm.Items) == 1 && *cm.Items[0].Key == "detected.json" && *cm.Items[0].Path == "detected.json"
	}
	if !vol {
		t.Error("expected an optional ConfigMap volume detected with the detected.json item")
	}
}

func TestDeploymentAC_NoDetectedWithoutConfigMap(t *testing.T) {
	deploy := deploymentAC(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}).(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]
	for _, e := range container.Env {
		if *e.Name == detectedEnvVar {
			t.Errorf("unexpected %s without a detected ConfigMap", detectedEnvVar)
		}
	}
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name == "detected" {
			t.Error("unexpected detected volume without a detected ConfigMap")
		}
	}
}

// The dashboard reads the installation's fingerprints as a kubelet-mounted
// file, so its ServiceAccount gains no ConfigMap rule.
func TestDeploymentAC_FingerprintsVolume(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", FingerprintsConfigMap: "pacto-operator-fingerprints"}
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]
	env := slices.ContainsFunc(container.Env, func(e corev1ac.EnvVarApplyConfiguration) bool {
		return *e.Name == fingerprintsEnvVar && *e.Value == "/etc/pacto/fingerprints/fingerprints.yaml"
	})
	mount := slices.ContainsFunc(container.VolumeMounts, func(vm corev1ac.VolumeMountApplyConfiguration) bool {
		return *vm.Name == "fingerprints" && *vm.MountPath == "/etc/pacto/fingerprints" && *vm.ReadOnly
	})
	vol := slices.ContainsFunc(deploy.Spec.Template.Spec.Volumes, func(v corev1ac.VolumeApplyConfiguration) bool {
		return *v.Name == "fingerprints" && v.ConfigMap != nil && *v.ConfigMap.Name == "pacto-operator-fingerprints" && *v.ConfigMap.Items[0].Key == "fingerprints.yaml"
	})
	if !env || !mount || !vol {
		t.Errorf("env %v mount %v volume %v", env, mount, vol)
	}
}

// The dashboard reads the chart's owner policy as a kubelet-mounted file from
// the immutable ConfigMap the operator names; without one it gets nothing.
func TestApplyMountsOwnerPolicy(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", OwnerPolicyConfigMap: "pacto-operator-owner-policy-0123456789"}
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]
	env := slices.ContainsFunc(container.Env, func(e corev1ac.EnvVarApplyConfiguration) bool {
		return *e.Name == "PACTO_DASHBOARD_OWNER_POLICY" && *e.Value == "/etc/pacto/owner-policy/policy.json"
	})
	mount := slices.ContainsFunc(container.VolumeMounts, func(vm corev1ac.VolumeMountApplyConfiguration) bool {
		return *vm.Name == "owner-policy" && *vm.MountPath == "/etc/pacto/owner-policy" && *vm.ReadOnly
	})
	vol := slices.ContainsFunc(deploy.Spec.Template.Spec.Volumes, func(v corev1ac.VolumeApplyConfiguration) bool {
		return *v.Name == "owner-policy" && v.ConfigMap != nil && *v.ConfigMap.Name == cfg.OwnerPolicyConfigMap &&
			*v.ConfigMap.Items[0].Key == "policy.json" && *v.ConfigMap.Optional
	})
	if !env || !mount || !vol {
		t.Errorf("env %v mount %v volume %v", env, mount, vol)
	}

	cfg.OwnerPolicyConfigMap = ""
	deploy = deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	for _, e := range deploy.Spec.Template.Spec.Containers[0].Env {
		if *e.Name == "PACTO_DASHBOARD_OWNER_POLICY" {
			t.Error("owner policy env without a ConfigMap")
		}
	}
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name == "owner-policy" {
			t.Error("owner policy volume without a ConfigMap")
		}
	}
}

// TestAuthEnvMCP: the dashboard serves /mcp only with a usable sign-in (token
// sign-in, or an issuer with an exchange audience and a URL), the chart's rule,
// so the operator never starts a dashboard that refuses --mcp.
func TestAuthEnvMCP(t *testing.T) {
	issuer := AuthConfig{MCP: true, IssuerURL: "https://idp", MCPExchangeAudience: "kube", MCPURL: "https://dash/mcp"}
	for _, c := range []struct {
		name string
		auth AuthConfig
		want map[string]string
	}{
		{"token login", AuthConfig{MCP: true, TokenLogin: true}, map[string]string{"PACTO_DASHBOARD_MCP": "true"}},
		{"issuer, exchange audience and URL", issuer, map[string]string{"PACTO_DASHBOARD_MCP": "true",
			"PACTO_DASHBOARD_MCP_URL": "https://dash/mcp", "PACTO_DASHBOARD_MCP_EXCHANGE_AUDIENCE": "kube"}},
		{"issuer without exchange audience", AuthConfig{MCP: true, IssuerURL: "https://idp", MCPURL: "https://dash/mcp"}, nil},
		{"issuer and audience without URL", AuthConfig{MCP: true, IssuerURL: "https://idp", MCPExchangeAudience: "kube"}, nil},
		{"anonymous only", AuthConfig{MCP: true, AllowAnonymous: true}, nil},
		{"off", AuthConfig{TokenLogin: true, MCPURL: "https://dash/mcp"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]string{}
			for _, e := range authEnv(Config{Auth: c.auth}) {
				if strings.HasPrefix(*e.Name, "PACTO_DASHBOARD_MCP") {
					got[*e.Name] = *e.Value
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("MCP env = %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
