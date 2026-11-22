package memberlink

// Resource is an API group and resource a review may name.
type Resource struct{ Group, Resource string }

// ReviewableResources is every resource the hub may ask a member about,
// keyed by the kind it decides: the Pactos every fleet answer needs, the
// workload kinds of inferred services and the GitOps kinds repositories are
// read from. A member refuses any other resource, so a hub gets no RBAC oracle
// over secrets or other objects.
var ReviewableResources = map[string]Resource{
	"Pacto":         {Group: "pacto.trianalab.io", Resource: "pactos"},
	"Deployment":    {Group: "apps", Resource: "deployments"},
	"StatefulSet":   {Group: "apps", Resource: "statefulsets"},
	"DaemonSet":     {Group: "apps", Resource: "daemonsets"},
	"CronJob":       {Group: "batch", Resource: "cronjobs"},
	"Application":   {Group: "argoproj.io", Resource: "applications"},
	"GitRepository": {Group: "source.toolkit.fluxcd.io", Resource: "gitrepositories"},
	"Kustomization": {Group: "kustomize.toolkit.fluxcd.io", Resource: "kustomizations"},
	"HelmRelease":   {Group: "helm.toolkit.fluxcd.io", Resource: "helmreleases"},
}

// Reviewable says whether group/resource is in ReviewableResources.
func Reviewable(group, resource string) bool {
	for _, r := range ReviewableResources {
		if r == (Resource{Group: group, Resource: resource}) {
			return true
		}
	}
	return false
}
