package k8sclient

import "k8s.io/apimachinery/pkg/runtime/schema"

// WorkloadGVRs are the resources of the workload kinds Pacto reads, by kind.
var WorkloadGVRs = map[string]schema.GroupVersionResource{
	"Deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"DaemonSet":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"CronJob":     {Group: "batch", Version: "v1", Resource: "cronjobs"},
}

// GitOpsResources are the Argo CD and Flux resources Pacto lists to name the
// repository behind a group, by kind. Discovery picks each one's version.
var GitOpsResources = map[string]schema.GroupResource{
	"Application":   {Group: "argoproj.io", Resource: "applications"},
	"Kustomization": {Group: "kustomize.toolkit.fluxcd.io", Resource: "kustomizations"},
	"HelmRelease":   {Group: "helm.toolkit.fluxcd.io", Resource: "helmreleases"},
	"GitRepository": {Group: "source.toolkit.fluxcd.io", Resource: "gitrepositories"},
}
