/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Key names a workload: its namespace, kind and name.
type Key struct {
	Namespace, Kind, Name string
}

// Owner is the workload a ReplicaSet or a Job runs for: a Deployment or a
// CronJob.
type Owner struct {
	Kind, Name string
}

// Digests groups the running images of pods by the workload that runs them:
// a pod controlled by a ReplicaSet or Job in owners counts for its owner, one
// controlled by a StatefulSet or DaemonSet for that controller; any other pod
// counts for nothing. A container whose image ID carries no repository digest
// is left out (unknown, never matched).
func Digests(pods []corev1.Pod, owners map[types.UID]Owner) map[Key][]RunningImage {
	out := map[Key][]RunningImage{}
	for i := range pods {
		p := &pods[i]
		k, ok := workloadOf(p, owners)
		if !ok {
			continue
		}
		ids := map[string]string{}
		for _, s := range p.Status.ContainerStatuses {
			ids[s.Name] = s.ImageID
		}
		for _, c := range p.Spec.Containers {
			digest, ok := digestOf(ids[c.Name])
			if !ok {
				continue
			}
			repo, tag := repositoryAndTag(c.Image)
			img := RunningImage{Repository: repo, Tag: tag, Digest: digest}
			if !slices.Contains(out[k], img) {
				out[k] = append(out[k], img)
			}
		}
	}
	return out
}

func workloadOf(p *corev1.Pod, owners map[types.UID]Owner) (Key, bool) {
	c := metav1.GetControllerOf(p)
	switch {
	case c == nil:
		return Key{}, false
	case c.Kind == "StatefulSet" || c.Kind == "DaemonSet":
		return Key{p.Namespace, c.Kind, c.Name}, true
	}
	o, ok := owners[c.UID]
	return Key{p.Namespace, o.Kind, o.Name}, ok
}

// digestOf is the repository digest of a container status imageID
// (docker-pullable://repo@sha256:<hex> or repo@sha256:<hex>). A bare
// sha256:<hex> is a config id, which no images record names.
func digestOf(imageID string) (string, bool) {
	_, digest, ok := strings.Cut(imageID, "@")
	return digest, ok && sha256RE.MatchString(digest)
}

// repositoryOf is image's repository with tag and digest removed and Docker
// Hub names spelled out, as pacto publish records an image repository.
func repositoryOf(image string) string {
	repo, _ := repositoryAndTag(image)
	return repo
}

func repositoryAndTag(image string) (repo, tag string) {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image, tag = image[:i], image[i+1:]
	}
	first, _, slash := strings.Cut(image, "/")
	if !slash {
		image = "library/" + image
	}
	if !slash || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		image = "docker.io/" + image
	}
	return image, tag
}
