/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ObservationMountRoot is the parent directory under which every configured
// observation source is mounted. Each source gets its own subdirectory named
// after the source, so mount paths are a function of declared identity alone —
// never of list position or of what the file happens to be called.
const ObservationMountRoot = "/var/lib/pacto/observation"

// ObservationEnvVar is the dashboard environment variable carrying the resolved
// sources. Its value is a whitespace-separated list of NAME=PATH pairs, which is
// exactly how the dashboard's configuration decodes a list from the environment.
const ObservationEnvVar = "PACTO_DASHBOARD_TRACE_SOURCES"

// observationVolumePrefix keeps managed observation volumes recognizable and
// collision-free against the dashboard's other volumes (cache, oci-creds).
const observationVolumePrefix = "obs-"

// maxObservationNameLength is what remains of a 63-character Kubernetes name once
// the volume prefix is spent. Names are length-checked against it rather than
// truncated: two long names truncated to the same volume name would be two Data
// Sources silently sharing one mount.
const maxObservationNameLength = validation.DNS1123LabelMaxLength - len(observationVolumePrefix)

// ObservationSource is one offline OTLP/JSON trace file the operator mounts into
// the dashboard read-only, declared under a stable name.
//
// The name is the identity: it becomes the dashboard's trace-source id, and
// through it the fleet source id and the Product Data Source. It is deliberately
// not derived from the file, the volume or the position in a list, so reordering
// or relocating the configuration never renames a Data Source.
//
// Exactly one backing supplies the file. Pacto never writes to it: the trace
// export is produced and rotated by whatever system owns that storage.
type ObservationSource struct {
	// Name is the stable Data Source identity (a DNS-1123 label).
	Name string

	// File is the trace file's path RELATIVE to this source's mount directory.
	File string

	// ExistingClaim is the name of an existing PersistentVolumeClaim holding the
	// trace export. The production-plausible backing: some other workload writes
	// the export, the dashboard only reads it.
	ExistingClaim string

	// ConfigMap is the name of a ConfigMap holding the trace export. Suitable only
	// for small, static exports (fixtures, deterministic tests) — a ConfigMap is
	// capped near 1 MiB and is not where real trace volume belongs.
	ConfigMap string
}

// Validate checks that a source can be turned into deterministic, safe wiring.
// Every rule fails closed: nothing here is repaired, because every repair would
// silently change which file a named Data Source reads.
func (o ObservationSource) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("observation source name must be set")
	}
	if len(o.Name) > maxObservationNameLength {
		return fmt.Errorf("observation source name %q is longer than %d characters", o.Name, maxObservationNameLength)
	}
	if errs := validation.IsDNS1123Label(o.Name); len(errs) > 0 {
		return fmt.Errorf("invalid observation source name %q: %s", o.Name, strings.Join(errs, "; "))
	}
	if o.File == "" {
		return fmt.Errorf("observation source %q must set file", o.Name)
	}
	// A relative, non-escaping path is the only thing that can address the mount:
	// an absolute path or a ".." segment would reach for the container filesystem
	// rather than the volume this source declares.
	if !filepath.IsLocal(o.File) {
		return fmt.Errorf("observation source %q file %q must be a relative path inside its mount", o.Name, o.File)
	}
	if strings.ContainsAny(o.File, " \t\n") {
		return fmt.Errorf("observation source %q file %q must not contain whitespace", o.Name, o.File)
	}
	switch {
	case o.ExistingClaim != "" && o.ConfigMap != "":
		return fmt.Errorf("observation source %q sets both existingClaim and configMap; exactly one backing is allowed", o.Name)
	case o.ExistingClaim == "" && o.ConfigMap == "":
		return fmt.Errorf("observation source %q must set exactly one of existingClaim or configMap", o.Name)
	}
	return nil
}

// VolumeName is the managed volume/mount name for this source.
func (o ObservationSource) VolumeName() string { return observationVolumePrefix + o.Name }

// MountPath is the directory this source's backing is mounted at.
func (o ObservationSource) MountPath() string { return path.Join(ObservationMountRoot, o.Name) }

// FilePath is the absolute in-container path of the trace file.
func (o ObservationSource) FilePath() string {
	return path.Join(o.MountPath(), filepath.ToSlash(o.File))
}

// SortedObservationSources returns the configured sources ordered by name, so the
// generated Deployment depends on the set of sources and not on the order they
// were written in. Reordering the Helm values must not roll the dashboard.
func (c Config) SortedObservationSources() []ObservationSource {
	out := slices.Clone(c.Observation)
	slices.SortFunc(out, func(a, b ObservationSource) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ObservationEnv returns the value of [ObservationEnvVar] for the configured
// sources, or "" when none are configured.
func (c Config) ObservationEnv() string {
	sources := c.SortedObservationSources()
	pairs := make([]string, 0, len(sources))
	for _, o := range sources {
		pairs = append(pairs, o.Name+"="+o.FilePath())
	}
	return strings.Join(pairs, " ")
}

// validateObservation checks every configured source and rejects duplicate names.
// Two sources sharing a name are two Data Sources claiming one identity — and,
// after prefixing, one Kubernetes volume; neither is repairable.
func (c Config) validateObservation() error {
	seen := make(map[string]struct{}, len(c.Observation))
	for _, o := range c.Observation {
		if err := o.Validate(); err != nil {
			return err
		}
		if _, dup := seen[o.Name]; dup {
			return fmt.Errorf("duplicate observation source name %q", o.Name)
		}
		seen[o.Name] = struct{}{}
	}
	return nil
}

// ParseObservationSource parses one --dashboard-trace-source flag value: a
// comma-separated list of key=value pairs, as in
// "name=orders,file=traces.json,existingClaim=orders-traces". The flat form keeps
// the controller's wire greppable in a Deployment spec and free of the quoting
// hazards an embedded JSON document would carry through Helm.
func ParseObservationSource(spec string) (ObservationSource, error) {
	var o ObservationSource
	for field := range strings.SplitSeq(spec, ",") {
		key, value, found := strings.Cut(field, "=")
		if !found {
			return ObservationSource{}, fmt.Errorf("invalid trace source %q: field %q is not key=value", spec, field)
		}
		switch strings.TrimSpace(key) {
		case "name":
			o.Name = value
		case "file":
			o.File = value
		case "existingClaim":
			o.ExistingClaim = value
		case "configMap":
			o.ConfigMap = value
		default:
			return ObservationSource{}, fmt.Errorf("invalid trace source %q: unknown field %q", spec, key)
		}
	}
	if err := o.Validate(); err != nil {
		return ObservationSource{}, err
	}
	return o, nil
}
