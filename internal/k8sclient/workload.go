package k8sclient

import "k8s.io/apimachinery/pkg/runtime/schema"

// WorkloadGVRs are the resources of the workload kinds Pacto reads, by kind.
var WorkloadGVRs = map[string]schema.GroupVersionResource{
	"Deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"DaemonSet":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"CronJob":     {Group: "batch", Version: "v1", Resource: "cronjobs"},
}
