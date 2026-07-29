/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1" //nolint:lll // Used by Config.OwnerRef field type
)

// Config holds the operator-managed Evidence Server configuration.
type Config struct {
	// Enabled controls whether the operator manages an Evidence Server deployment.
	Enabled bool

	// Image is the runtime container image (the pacto binary) that runs
	// `pacto evidence serve`. Set at build time to couple it to the Pacto library
	// version, same as the dashboard image. Not user-configurable.
	Image string

	// Namespace is the operator's own namespace, where evidence resources live.
	Namespace string

	// BucketURL is the gocloud.dev bucket URL. The default file:// needs no
	// external infrastructure; s3://, gs:// and azblob:// use cloud storage with
	// the same evidence logic.
	BucketURL string

	// Prefix scopes every object key below it, so installations can safely share a
	// bucket via distinct prefixes.
	Prefix string

	// TrustSecret is the name of a Secret of trusted producer public keys, mounted
	// read-only. Signature verification is mandatory, so it is required.
	TrustSecret string

	// Persistence configures the PVC backing a file:// bucket.
	Persistence PersistenceConfig

	// Resources overrides the container's resource requirements.
	Resources ResourcesConfig

	// OwnerRef is an optional owner reference to the operator's own Deployment.
	// It is attached to the Deployment and Service so a GitOps tool shows them in
	// the resource tree — but NEVER to the PVC, which must survive the operator.
	OwnerRef *metav1ac.OwnerReferenceApplyConfiguration
}

// PersistenceConfig configures the evidence PVC. Persistent evidence is retained
// by default; the PVC is never garbage-collected with the operator.
type PersistenceConfig struct {
	// Enabled provisions a PVC for a file:// bucket. Ignored when ExistingClaim
	// is set or the bucket is cloud-backed.
	Enabled bool
	// ExistingClaim uses an externally-managed PVC instead of provisioning one.
	ExistingClaim string
	// Size is the requested storage (e.g. 1Gi).
	Size string
	// StorageClass is the optional storage class.
	StorageClass string
	// AccessModes default to [ReadWriteOnce] (single writer).
	AccessModes []string
	// Retain keeps the PVC on component disable and uninstall (default true).
	Retain bool
}

// ClaimName returns the PVC name to mount: the existing claim when set, else the
// managed default.
func (p PersistenceConfig) ClaimName() string {
	if p.ExistingClaim != "" {
		return p.ExistingClaim
	}
	return PVCName
}

// ResourcesConfig holds optional resource quantity overrides.
type ResourcesConfig struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// Validate checks that any provided resource quantity overrides are parseable so
// a bad --evidence-cpu-request (etc.) fails fast at startup.
func (rc ResourcesConfig) Validate() error {
	for _, q := range []struct{ name, val string }{
		{"cpu-request", rc.CPURequest},
		{"cpu-limit", rc.CPULimit},
		{"memory-request", rc.MemoryRequest},
		{"memory-limit", rc.MemoryLimit},
	} {
		if q.val == "" {
			continue
		}
		if _, err := resource.ParseQuantity(q.val); err != nil {
			return fmt.Errorf("invalid evidence %s quantity %q: %w", q.name, q.val, err)
		}
	}
	return nil
}

// DefaultResources returns the built-in default resource requirements.
func DefaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("25m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// BuildResources returns resource requirements, applying any overrides.
func (rc ResourcesConfig) BuildResources() corev1.ResourceRequirements {
	res := DefaultResources()
	if rc.CPURequest != "" {
		res.Requests[corev1.ResourceCPU] = resource.MustParse(rc.CPURequest)
	}
	if rc.MemoryRequest != "" {
		res.Requests[corev1.ResourceMemory] = resource.MustParse(rc.MemoryRequest)
	}
	if rc.CPULimit != "" {
		res.Limits[corev1.ResourceCPU] = resource.MustParse(rc.CPULimit)
	}
	if rc.MemoryLimit != "" {
		res.Limits[corev1.ResourceMemory] = resource.MustParse(rc.MemoryLimit)
	}
	return res
}

// Validate checks the config is valid when the feature is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Image == "" {
		return fmt.Errorf("evidence image must be set at build time via ldflags")
	}
	if hasLatestTag(c.Image) {
		return fmt.Errorf("evidence image must not use 'latest' tag: %s", c.Image)
	}
	if c.Namespace == "" {
		return fmt.Errorf("evidence namespace must be set (defaults to operator namespace)")
	}
	if c.TrustSecret == "" {
		return fmt.Errorf("evidence enabled but no trust secret set: signature verification is mandatory (set --evidence-trust-secret)")
	}
	if err := validatePrefix(c.Prefix); err != nil {
		return err
	}
	if isFileBucket(c.BucketURL) && !c.Persistence.Enabled && c.Persistence.ExistingClaim == "" {
		return fmt.Errorf("file:// evidence storage requires a PVC: set persistence.enabled or persistence.existingClaim")
	}
	return c.Resources.Validate()
}

// isFileBucket reports whether the bucket URL is a local file bucket.
func isFileBucket(bucketURL string) bool {
	return strings.HasPrefix(bucketURL, "file://")
}

// validatePrefix rejects an unsafe object-key prefix (absolute path or parent
// traversal). It mirrors the store's NormalizePrefix without importing it (the
// operator integration must not pull the pure-domain gocloud package chain).
func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("evidence prefix %q must be relative, not absolute", prefix)
	}
	for _, part := range strings.Split(prefix, "/") {
		if part == ".." {
			return fmt.Errorf("evidence prefix %q must not contain parent traversal", prefix)
		}
	}
	return nil
}

// hasLatestTag reports whether an image ref uses the :latest tag or no tag.
func hasLatestTag(image string) bool {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			return image[i+1:] == "latest"
		}
		if image[i] == '/' {
			break
		}
	}
	return true
}
