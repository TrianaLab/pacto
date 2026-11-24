/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
)

// serviceAccountAC returns a server-side apply configuration for the dashboard ServiceAccount.
func serviceAccountAC(cfg Config) runtime.ApplyConfiguration {
	sa := corev1ac.ServiceAccount(Name, cfg.Namespace).
		WithLabels(Labels())
	if cfg.OwnerRef != nil {
		sa.WithOwnerReferences(cfg.OwnerRef)
	}
	return sa
}

// clusterRoleAC returns a server-side apply configuration for the dashboard ClusterRole. A scoped
// dashboard (a namespace list with delivery on) gets only the delivery half: the chart grants it
// Pactos, PactoRevisions and Services in each watched namespace.
func clusterRoleAC(scoped bool) runtime.ApplyConfiguration {
	var rules []*rbacv1ac.PolicyRuleApplyConfiguration
	if !scoped {
		rules = append(rules,
			rbacv1ac.PolicyRule().
				WithAPIGroups("pacto.trianalab.io").
				WithResources("pactos", "pactorevisions").
				WithVerbs("get", "list", "watch"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("services").
				WithVerbs("get", "list", "watch"),
			// Inferred services: the dashboard lists every workload; it never reads a Pod or a Secret and drops env values before anything is stored.
			rbacv1ac.PolicyRule().
				WithAPIGroups("apps").
				WithResources("deployments", "statefulsets", "daemonsets").
				WithVerbs("list"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("batch").
				WithResources("cronjobs").
				WithVerbs("list"),
			// Service grouping and repository provenance: the dashboard lists the GitOps objects that group inferred services; it reads no Secret or Helm release.
			rbacv1ac.PolicyRule().
				WithAPIGroups("argoproj.io").
				WithResources("applications").
				WithVerbs("list"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("kustomize.toolkit.fluxcd.io").
				WithResources("kustomizations").
				WithVerbs("list"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("helm.toolkit.fluxcd.io").
				WithResources("helmreleases").
				WithVerbs("list"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("source.toolkit.fluxcd.io").
				WithResources("gitrepositories").
				WithVerbs("list"),
		)
	}
	return rbacv1ac.ClusterRole(Name).
		WithLabels(Labels()).
		WithRules(append(rules,
			// With sign-in, the dashboard keeps one copy of the delivery objects
			// people read and shows each person only what their own RBAC lets
			// them read; every action goes with the person's own token.
			rbacv1ac.PolicyRule().
				WithAPIGroups("delivery.pacto.trianalab.io").
				WithResources("stages", "operations").
				WithVerbs("get", "list", "watch"),
			// It asks the apiserver what a signed-in person may read.
			rbacv1ac.PolicyRule().
				WithAPIGroups("authorization.k8s.io").
				WithResources("subjectaccessreviews").
				WithVerbs("create"),
			// Delivery asks, per namespace, where a signed-in person may list
			// Stages, so it lists the namespaces' names to ask about.
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("namespaces").
				WithVerbs("list"),
			// Its live source names the cluster by the kube-system namespace
			// UID, so it reads that one namespace by name.
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("namespaces").
				WithResourceNames("kube-system").
				WithVerbs("get"),
		)...)
}

// clusterRoleBindingAC returns a server-side apply configuration for the dashboard ClusterRoleBinding.
func clusterRoleBindingAC(cfg Config) runtime.ApplyConfiguration {
	return rbacv1ac.ClusterRoleBinding(Name).
		WithLabels(Labels()).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind("ServiceAccount").
				WithName(Name).
				WithNamespace(cfg.Namespace),
		).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup("rbac.authorization.k8s.io").
				WithKind("ClusterRole").
				WithName(Name),
		)
}

// authEnv passes the sign-in and Declare settings to the dashboard; client
// secrets only by secretKeyRef.
func authEnv(cfg Config) []*corev1ac.EnvVarApplyConfiguration {
	var env []*corev1ac.EnvVarApplyConfiguration
	add := func(name, value string) {
		if value != "" {
			env = append(env, corev1ac.EnvVar().WithName(name).WithValue(value))
		}
	}
	a := cfg.Auth
	add("PACTO_DASHBOARD_OIDC_ISSUER_URL", a.IssuerURL)
	add("PACTO_DASHBOARD_OIDC_INTERNAL_ISSUER_URL", a.InternalIssuerURL)
	add("PACTO_DASHBOARD_OIDC_DISPLAY_NAME", a.DisplayName)
	if a.CAConfigMap != "" || a.CASecret != "" {
		add("PACTO_DASHBOARD_OIDC_CA_FILE", oidcCADir+"/"+keyOr(a.CAKey, "ca.crt"))
	}
	add("PACTO_DASHBOARD_OIDC_CLIENT_ID", a.ClientID)
	add("PACTO_DASHBOARD_OIDC_REDIRECT_URL", a.RedirectURL)
	add("PACTO_DASHBOARD_OIDC_EXTRA_SCOPES", strings.Join(a.ExtraScopes, " "))
	add("PACTO_DASHBOARD_OIDC_USERNAME_CLAIM", a.UsernameClaim)
	add("PACTO_DASHBOARD_OIDC_USERNAME_PREFIX", a.UsernamePrefix)
	add("PACTO_DASHBOARD_OIDC_GROUPS_CLAIM", a.GroupsClaim)
	add("PACTO_DASHBOARD_OIDC_GROUPS_PREFIX", a.GroupsPrefix)
	if a.SessionIdleTimeout > 0 {
		add("PACTO_DASHBOARD_SESSION_IDLE_TIMEOUT", a.SessionIdleTimeout.String())
	}
	if a.SessionMaxAge > 0 {
		add("PACTO_DASHBOARD_SESSION_MAX_AGE", a.SessionMaxAge.String())
	}
	if a.TokenLogin {
		add("PACTO_DASHBOARD_TOKEN_LOGIN", "true")
	}
	// The image binds beyond loopback, so without sign-in the dashboard starts
	// only when the install chose anonymous access.
	if a.AllowAnonymous && a.IssuerURL == "" && !a.TokenLogin {
		add("PACTO_DASHBOARD_ALLOW_ANONYMOUS", "true")
	}
	if a.mcpServed() {
		add("PACTO_DASHBOARD_MCP", "true")
		add("PACTO_DASHBOARD_MCP_URL", a.MCPURL)
		add("PACTO_DASHBOARD_MCP_EXCHANGE_AUDIENCE", a.MCPExchangeAudience)
	}
	if a.ClientSecretName != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_OIDC_CLIENT_SECRET").WithValueFrom(
			corev1ac.EnvVarSource().WithSecretKeyRef(corev1ac.SecretKeySelector().WithName(a.ClientSecretName).
				WithKey(keyOr(a.ClientSecretKey, "client-secret")))))
	}
	return append(env, declareEnv(cfg)...)
}

// declareEnv passes how the dashboard signs and publishes declarations: the
// signing client (its secret by secretKeyRef), the Sigstore, the registry and
// the path of the push credential mounted by authVolumes.
func declareEnv(cfg Config) []*corev1ac.EnvVarApplyConfiguration {
	var env []*corev1ac.EnvVarApplyConfiguration
	a := cfg.Auth
	push := ""
	if cfg.DeclareRegistrySecret != "" {
		push = declareRegistryDir + "/config.json"
	}
	for _, kv := range [][2]string{{"PACTO_DASHBOARD_SIGN_CLIENT_ID", a.SignClientID},
		{"PACTO_DASHBOARD_SIGN_REDIRECT_URL", a.SignRedirectURL}, {"PACTO_DASHBOARD_FULCIO_URL", cfg.FulcioURL},
		{"PACTO_DASHBOARD_REKOR_URL", cfg.RekorURL}, {"PACTO_DASHBOARD_TIMESTAMP_URL", cfg.TimestampURL},
		{"PACTO_DASHBOARD_REGISTRY", cfg.Registry}, {"PACTO_DASHBOARD_DECLARE_REGISTRY_CONFIG", push}} {
		if kv[1] != "" {
			env = append(env, corev1ac.EnvVar().WithName(kv[0]).WithValue(kv[1]))
		}
	}
	if a.SignClientSecretName != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_SIGN_CLIENT_SECRET").WithValueFrom(corev1ac.EnvVarSource().
			WithSecretKeyRef(corev1ac.SecretKeySelector().WithName(a.SignClientSecretName).
				WithKey(keyOr(a.SignClientSecretKey, "client-secret")))))
	}
	return env
}

// mcpServed is the chart's usable-sign-in rule: the dashboard refuses --mcp
// without one.
func (a AuthConfig) mcpServed() bool {
	return a.MCP && (a.TokenLogin || (a.IssuerURL != "" && a.MCPExchangeAudience != "" && a.MCPURL != ""))
}

// deliveryEnv passes what the delivery and Metrics views read: the namespace
// delivery runs in, the trust config its records are verified with, Mira's
// address, the stats repository and the organization index.
func deliveryEnv(cfg Config) []*corev1ac.EnvVarApplyConfiguration {
	trust := ""
	if cfg.DeliveryTrust.Name != "" {
		trust = deliveryTrustDir + "/" + keyOr(cfg.DeliveryTrust.Key, "trust.yaml")
	}
	var env []*corev1ac.EnvVarApplyConfiguration
	for _, kv := range [][2]string{{"PACTO_DASHBOARD_DELIVERY_NAMESPACE", cfg.DeliveryNamespace},
		{"PACTO_DASHBOARD_DELIVERY_TRUST", trust}, {"PACTO_DASHBOARD_MIRA_URL", cfg.MiraURL},
		{"PACTO_STATS_REGISTRY", cfg.StatsRegistry}, {"PACTO_DASHBOARD_INDEX", cfg.Index}} {
		if kv[1] != "" {
			env = append(env, corev1ac.EnvVar().WithName(kv[0]).WithValue(kv[1]))
		}
	}
	return append(env, backstageEnv(cfg)...)
}

// backstageEnv passes the Backstage catalog URL and, by a required
// secretKeyRef the kubelet resolves, its reader token: a named Secret that is
// missing keeps the pod pending. The token goes only with a configured URL,
// never to a detected catalog.
func backstageEnv(cfg Config) []*corev1ac.EnvVarApplyConfiguration {
	if cfg.BackstageURL == "" {
		return nil
	}
	env := []*corev1ac.EnvVarApplyConfiguration{corev1ac.EnvVar().WithName("PACTO_DASHBOARD_BACKSTAGE_URL").WithValue(cfg.BackstageURL)}
	if cfg.BackstageToken.Name != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_BACKSTAGE_TOKEN").WithValueFrom(corev1ac.EnvVarSource().
			WithSecretKeyRef(corev1ac.SecretKeySelector().WithName(cfg.BackstageToken.Name).
				WithKey(keyOr(cfg.BackstageToken.Key, "token")).WithOptional(false))))
	}
	return env
}

// Where the issuer CA bundle and the delivery trust config mount.
const (
	oidcCADir        = "/etc/pacto/oidc-ca"
	deliveryTrustDir = "/etc/pacto/delivery-trust"
	// The Declare writer's push credential: its own directory, never the read
	// keychain at /home/pacto/.docker.
	declareRegistryDir = "/etc/pacto/declare-registry"
)

func keyOr(key, def string) string {
	if key == "" {
		return def
	}
	return key
}

// authVolumes mounts the issuer CA bundle and the delivery trust config, each
// read-only and only the key the dashboard reads.
func authVolumes(cfg Config) ([]*corev1ac.VolumeMountApplyConfiguration, []*corev1ac.VolumeApplyConfiguration) {
	var mounts []*corev1ac.VolumeMountApplyConfiguration
	var vols []*corev1ac.VolumeApplyConfiguration
	add := func(name, dir string, v *corev1ac.VolumeApplyConfiguration) {
		mounts = append(mounts, corev1ac.VolumeMount().WithName(name).WithMountPath(dir).WithReadOnly(true))
		vols = append(vols, v.WithName(name))
	}
	a := cfg.Auth
	if key := keyOr(a.CAKey, "ca.crt"); a.CAConfigMap != "" {
		add("oidc-ca", oidcCADir, corev1ac.Volume().WithConfigMap(corev1ac.ConfigMapVolumeSource().WithName(a.CAConfigMap).
			WithItems(corev1ac.KeyToPath().WithKey(key).WithPath(key))))
	} else if a.CASecret != "" {
		add("oidc-ca", oidcCADir, corev1ac.Volume().WithSecret(corev1ac.SecretVolumeSource().WithSecretName(a.CASecret).
			WithItems(corev1ac.KeyToPath().WithKey(key).WithPath(key))))
	}
	if t := cfg.DeliveryTrust; t.Name != "" {
		key := keyOr(t.Key, "trust.yaml")
		add("delivery-trust", deliveryTrustDir, corev1ac.Volume().WithConfigMap(corev1ac.ConfigMapVolumeSource().WithName(t.Name).
			WithItems(corev1ac.KeyToPath().WithKey(key).WithPath(key))))
	}
	// Not optional: the dashboard refuses to start on a missing push
	// credential, so the pod waits for the Secret instead.
	if s := cfg.DeclareRegistrySecret; s != "" {
		add("declare-registry", declareRegistryDir, corev1ac.Volume().WithSecret(corev1ac.SecretVolumeSource().WithSecretName(s).
			WithItems(corev1ac.KeyToPath().WithKey(string(corev1.DockerConfigJsonKey)).WithPath("config.json"))))
	}
	return mounts, vols
}

// The managed dashboard reads what the operator detected from this file.
const (
	detectedEnvVar    = "PACTO_DASHBOARD_DETECTED"
	detectedMountPath = "/etc/pacto/detected"
	detectedKey       = "detected.json"
)

// The managed dashboard reads the installation's fingerprints from this file.
const (
	fingerprintsEnvVar    = "PACTO_DASHBOARD_FINGERPRINTS"
	fingerprintsMountPath = "/etc/pacto/fingerprints"
	fingerprintsKey       = "fingerprints.yaml"
)

// The managed dashboard reads the chart's owner policy from this file.
const (
	ownerPolicyEnvVar    = "PACTO_DASHBOARD_OWNER_POLICY"
	ownerPolicyMountPath = "/etc/pacto/owner-policy"
	ownerPolicyKey       = "policy.json"
)

// The managed dashboard verifies declarations with the trusted root in this file.
const (
	trustedRootEnvVar    = "PACTO_DASHBOARD_TRUSTED_ROOT"
	trustedRootMountPath = "/etc/pacto/trusted-root"
	trustedRootKey       = "trusted_root.json"
)

// deploymentAC returns a server-side apply configuration for the dashboard Deployment.
func deploymentAC(cfg Config) runtime.ApplyConfiguration {
	env := []*corev1ac.EnvVarApplyConfiguration{
		corev1ac.EnvVar().WithName("PACTO_DASHBOARD_PORT").WithValue("3000"),
		corev1ac.EnvVar().WithName("PACTO_NO_UPDATE_CHECK").WithValue("1"),
		corev1ac.EnvVar().WithName("PACTO_DASHBOARD_OTLP_RECEIVER").
			WithValue(fmt.Sprintf("http://%s.%s:%d", Name, cfg.Namespace, OTLPPort)),
	}
	if len(cfg.WatchNamespaces) > 0 {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_NAMESPACE").WithValue(strings.Join(cfg.WatchNamespaces, ",")))
	}
	if !cfg.needsClusterRole() {
		// Without a ClusterRole the dashboard can only ask, namespace by namespace, what a
		// signed-in person may read: the chart grants localsubjectaccessreviews there.
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_REVIEW").WithValue("local"))
	}
	env = append(append(env, authEnv(cfg)...), deliveryEnv(cfg)...)
	if cfg.EvidenceSourceURL != nil {
		if url := cfg.EvidenceSourceURL(); url != "" {
			env = append(env, corev1ac.EnvVar().WithName("PACTO_EVIDENCE_SOURCE_URL").WithValue(url))
		}
	}
	if cfg.InsecureRegistries != "" {
		env = append(env, corev1ac.EnvVar().WithName(loader.InsecureRegistriesEnvVar).WithValue(cfg.InsecureRegistries))
	}
	if len(cfg.Repositories) > 0 {
		// The dashboard takes PACTO_DASHBOARD_REPO entries raw (only its
		// arguments get the scheme stripped), so trim oci:// here.
		repos := make([]string, len(cfg.Repositories))
		for i, r := range cfg.Repositories {
			repos[i] = strings.TrimPrefix(r, "oci://")
		}
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_REPO").WithValue(strings.Join(repos, ",")))
	}
	if cfg.ExternalCatalog != "" {
		// Passed verbatim: the dashboard strips oci:// from it itself.
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_EXTERNAL_CATALOG").WithValue(cfg.ExternalCatalog))
	}
	if obs := cfg.ObservationEnv(); obs != "" {
		env = append(env, corev1ac.EnvVar().WithName(ObservationEnvVar).WithValue(obs))
	}

	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		corev1ac.VolumeMount().WithName("cache").WithMountPath("/home/pacto/.cache/pacto"),
	}
	volumes := []*corev1ac.VolumeApplyConfiguration{
		corev1ac.Volume().WithName("cache").WithEmptyDir(corev1ac.EmptyDirVolumeSource()),
	}

	// Observation sources mount read-only: the dashboard is a reader of someone
	// else's trace export, never its owner.
	for _, o := range cfg.SortedObservationSources() {
		volumeMounts = append(volumeMounts,
			corev1ac.VolumeMount().WithName(o.VolumeName()).WithMountPath(o.MountPath()).WithReadOnly(true),
		)
		vol := corev1ac.Volume().WithName(o.VolumeName())
		if o.ConfigMap != "" {
			// Optional: a ConfigMap that has not been created yet must degrade the
			// Data Source, not wedge the pod in ContainerCreating. A missing PVC
			// still blocks scheduling — that is Kubernetes' call, not ours.
			vol.WithConfigMap(corev1ac.ConfigMapVolumeSource().WithName(o.ConfigMap).WithOptional(true))
		} else {
			vol.WithPersistentVolumeClaim(
				corev1ac.PersistentVolumeClaimVolumeSource().WithClaimName(o.ExistingClaim).WithReadOnly(true),
			)
		}
		volumes = append(volumes, vol)
	}

	authMounts, authVols := authVolumes(cfg)
	volumeMounts, volumes = append(volumeMounts, authMounts...), append(volumes, authVols...)

	fileEnv, fileMounts, fileVols := configMapFiles(cfg)
	env, volumeMounts, volumes = append(env, fileEnv...), append(volumeMounts, fileMounts...), append(volumes, fileVols...)

	if len(cfg.OCISecrets) > 0 {
		volumeMounts = append(volumeMounts,
			corev1ac.VolumeMount().WithName("oci-creds").WithMountPath("/home/pacto/.docker").WithReadOnly(true),
		)
		volumes = append(volumes,
			corev1ac.Volume().WithName("oci-creds").WithSecret(
				corev1ac.SecretVolumeSource().
					WithSecretName(ManagedSecretName).
					WithItems(corev1ac.KeyToPath().WithKey(string(corev1.DockerConfigJsonKey)).WithPath("config.json")).
					WithOptional(true),
			),
		)
	}

	res := cfg.Resources.Build(DefaultResources())

	container := corev1ac.Container().
		WithName("dashboard").
		WithImage(cfg.Image).
		WithPorts(
			corev1ac.ContainerPort().WithName("http").WithContainerPort(DashboardPort).WithProtocol(corev1.ProtocolTCP),
			corev1ac.ContainerPort().WithName("otlp-http").WithContainerPort(OTLPPort).WithProtocol(corev1.ProtocolTCP),
		).
		WithEnv(env...).
		WithVolumeMounts(volumeMounts...).
		WithLivenessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(HealthPath).WithPort(intstr.FromInt32(DashboardPort))).
				WithInitialDelaySeconds(10).
				WithPeriodSeconds(30),
		).
		WithReadinessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(HealthPath).WithPort(intstr.FromInt32(DashboardPort))).
				WithInitialDelaySeconds(5).
				WithPeriodSeconds(10),
		).
		WithSecurityContext(component.RestrictedContainerSecurityContext()).
		WithResources(corev1ac.ResourceRequirements().WithRequests(res.Requests).WithLimits(res.Limits))

	deploy := appsv1ac.Deployment(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(appsv1ac.DeploymentSpec().
			WithReplicas(1).
			WithSelector(metav1ac.LabelSelector().WithMatchLabels(SelectorLabels())).
			WithTemplate(corev1ac.PodTemplateSpec().
				WithLabels(Labels()).
				WithSpec(corev1ac.PodSpec().
					WithServiceAccountName(Name).
					WithSecurityContext(
						corev1ac.PodSecurityContext().
							WithRunAsNonRoot(true).
							WithRunAsUser(65532).
							WithSeccompProfile(corev1ac.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault)),
					).
					WithContainers(container).
					WithVolumes(volumes...).
					WithTerminationGracePeriodSeconds(10),
				),
			),
		)
	if cfg.OwnerRef != nil {
		deploy.WithOwnerReferences(cfg.OwnerRef)
	}
	return deploy
}

// serviceAC returns a server-side apply configuration for the dashboard Service.
func serviceAC(cfg Config) runtime.ApplyConfiguration {
	svc := corev1ac.Service(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(corev1ac.ServiceSpec().
			WithSelector(SelectorLabels()).
			WithPorts(
				corev1ac.ServicePort().
					WithName("http").
					WithPort(DashboardPort).
					WithTargetPort(intstr.FromInt32(DashboardPort)).
					WithProtocol(corev1.ProtocolTCP),
				corev1ac.ServicePort().
					WithName("otlp-http").
					WithPort(OTLPPort).
					WithTargetPort(intstr.FromInt32(OTLPPort)).
					WithProtocol(corev1.ProtocolTCP),
			),
		)
	if cfg.OwnerRef != nil {
		svc.WithOwnerReferences(cfg.OwnerRef)
	}
	return svc
}

// ociSecretAC returns a server-side apply configuration for the managed OCI credentials Secret.
func ociSecretAC(cfg Config, data []byte) runtime.ApplyConfiguration {
	secret := corev1ac.Secret(ManagedSecretName, cfg.Namespace).
		WithLabels(Labels()).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(map[string][]byte{
			string(corev1.DockerConfigJsonKey): data,
		})
	if cfg.OwnerRef != nil {
		secret.WithOwnerReferences(cfg.OwnerRef)
	}
	return secret
}

// configMapFiles are the operator-namespace ConfigMaps the dashboard reads as
// kubelet-mounted files (detection, fingerprints, the owner policy), so its
// ServiceAccount gains no ConfigMap rule.
func configMapFiles(cfg Config) ([]*corev1ac.EnvVarApplyConfiguration, []*corev1ac.VolumeMountApplyConfiguration,
	[]*corev1ac.VolumeApplyConfiguration) {
	var env []*corev1ac.EnvVarApplyConfiguration
	var mounts []*corev1ac.VolumeMountApplyConfiguration
	var vols []*corev1ac.VolumeApplyConfiguration
	if cfg.DetectedConfigMap != "" {
		env = append(env, corev1ac.EnvVar().WithName(detectedEnvVar).WithValue(detectedMountPath+"/"+detectedKey))
		mounts = append(mounts,
			corev1ac.VolumeMount().WithName("detected").WithMountPath(detectedMountPath).WithReadOnly(true))
		// Optional: before the first detection the pod still starts and the
		// dashboard names the detection source unavailable.
		vols = append(vols, corev1ac.Volume().WithName("detected").WithConfigMap(
			corev1ac.ConfigMapVolumeSource().WithName(cfg.DetectedConfigMap).
				WithItems(corev1ac.KeyToPath().WithKey(detectedKey).WithPath(detectedKey),
					// The linker's key, read from beside the detection file.
					corev1ac.KeyToPath().WithKey(core.LinkedConfigMapKey).WithPath(core.LinkedConfigMapKey),
					// The candidate clusters, beside the detection file.
					corev1ac.KeyToPath().WithKey(detect.ClustersConfigMapKey).WithPath(detect.ClustersConfigMapKey)).
				WithOptional(true)))
	}

	if cfg.FingerprintsConfigMap != "" {
		env = append(env, corev1ac.EnvVar().WithName(fingerprintsEnvVar).WithValue(fingerprintsMountPath+"/"+fingerprintsKey))
		mounts = append(mounts,
			corev1ac.VolumeMount().WithName("fingerprints").WithMountPath(fingerprintsMountPath).WithReadOnly(true))
		vols = append(vols, corev1ac.Volume().WithName("fingerprints").WithConfigMap(
			corev1ac.ConfigMapVolumeSource().WithName(cfg.FingerprintsConfigMap).
				WithItems(corev1ac.KeyToPath().WithKey(fingerprintsKey).WithPath(fingerprintsKey))))
	}

	if cfg.OwnerPolicyConfigMap != "" {
		env = append(env, corev1ac.EnvVar().WithName(ownerPolicyEnvVar).WithValue(ownerPolicyMountPath+"/"+ownerPolicyKey))
		mounts = append(mounts,
			corev1ac.VolumeMount().WithName("owner-policy").WithMountPath(ownerPolicyMountPath).WithReadOnly(true))
		// Optional: a missing policy leaves owner changes unverified rather
		// than wedging the pod in ContainerCreating.
		vols = append(vols, corev1ac.Volume().WithName("owner-policy").WithConfigMap(
			corev1ac.ConfigMapVolumeSource().WithName(cfg.OwnerPolicyConfigMap).
				WithItems(corev1ac.KeyToPath().WithKey(ownerPolicyKey).WithPath(ownerPolicyKey)).
				WithOptional(true)))
	}

	if cfg.TrustedRootConfigMap != "" {
		env = append(env, corev1ac.EnvVar().WithName(trustedRootEnvVar).WithValue(trustedRootMountPath+"/"+trustedRootKey))
		mounts = append(mounts,
			corev1ac.VolumeMount().WithName("trusted-root").WithMountPath(trustedRootMountPath).WithReadOnly(true))
		vols = append(vols, corev1ac.Volume().WithName("trusted-root").WithConfigMap(
			corev1ac.ConfigMapVolumeSource().WithName(cfg.TrustedRootConfigMap).
				WithItems(corev1ac.KeyToPath().WithKey(trustedRootKey).WithPath(trustedRootKey))))
	}
	return env, mounts, vols
}
