/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"strings"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
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

// clusterRoleAC returns a server-side apply configuration for the dashboard ClusterRole.
func clusterRoleAC() runtime.ApplyConfiguration {
	return rbacv1ac.ClusterRole(Name).
		WithLabels(Labels()).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("pacto.trianalab.io").
				WithResources("pactos", "pactorevisions").
				WithVerbs("get", "list", "watch"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("services").
				WithVerbs("get", "list", "watch"),
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
		)
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

// authEnv passes the sign-in settings to the dashboard; the client secret only
// by secretKeyRef.
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
	if cfg.DeliveryTrust.Name != "" {
		add("PACTO_DASHBOARD_DELIVERY_TRUST", deliveryTrustDir+"/"+keyOr(cfg.DeliveryTrust.Key, "trust.yaml"))
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
	add("PACTO_DASHBOARD_DELIVERY_NAMESPACE", a.DeliveryNamespace)
	add("PACTO_DASHBOARD_MIRA_URL", cfg.MiraURL)
	if a.ClientSecretName != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_OIDC_CLIENT_SECRET").WithValueFrom(
			corev1ac.EnvVarSource().WithSecretKeyRef(corev1ac.SecretKeySelector().WithName(a.ClientSecretName).
				WithKey(keyOr(a.ClientSecretKey, "client-secret")))))
	}
	return env
}

// Where the issuer CA bundle and the delivery trust config mount.
const (
	oidcCADir        = "/etc/pacto/oidc-ca"
	deliveryTrustDir = "/etc/pacto/delivery-trust"
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
	return mounts, vols
}

// deploymentAC returns a server-side apply configuration for the dashboard Deployment.
func deploymentAC(cfg Config) runtime.ApplyConfiguration {
	env := []*corev1ac.EnvVarApplyConfiguration{
		corev1ac.EnvVar().WithName("PACTO_DASHBOARD_PORT").WithValue("3000"),
		corev1ac.EnvVar().WithName("PACTO_NO_UPDATE_CHECK").WithValue("1"),
	}
	if cfg.WatchNamespace != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_DASHBOARD_NAMESPACE").WithValue(cfg.WatchNamespace))
	}
	env = append(env, authEnv(cfg)...)
	if cfg.EvidenceSourceURL != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_EVIDENCE_SOURCE_URL").WithValue(cfg.EvidenceSourceURL))
	}
	if cfg.InsecureRegistries != "" {
		env = append(env, corev1ac.EnvVar().WithName(loader.InsecureRegistriesEnvVar).WithValue(cfg.InsecureRegistries))
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
		WithSecurityContext(
			corev1ac.SecurityContext().
				WithReadOnlyRootFilesystem(true).
				WithAllowPrivilegeEscalation(false).
				WithCapabilities(corev1ac.Capabilities().WithDrop("ALL")),
		).
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
