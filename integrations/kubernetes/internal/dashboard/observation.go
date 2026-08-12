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

// fieldSep separates the fields of one --dashboard-trace-source flag value. It is
// the one character no field may contain, which is what makes the flat wire
// injective: every value the chart accepts parses back to the same source.
const fieldSep = ","

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

	// File is the trace file's name DIRECTLY inside this source's mount
	// directory — a single path segment, never a nested path. Two things depend
	// on that: the dashboard derives the source's read root from the file's
	// parent directory, which is only the declared mount when nothing sits
	// between them (see [ObservationSource.FilePath]); and a nested path would
	// need directory separators the flat flag wire has no reason to carry. Mount
	// a claim whose export already sits at the top of its own directory.
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
	if strings.Contains(o.File, "/") {
		return fmt.Errorf("observation source %q file %q must be a plain file name directly inside its mount, not a nested path", o.Name, o.File)
	}
	if strings.ContainsAny(o.File, " \t\n") {
		return fmt.Errorf("observation source %q file %q must not contain whitespace", o.Name, o.File)
	}
	// The controller flag that carries this source is comma-delimited, so a comma
	// in any field would split one source into two malformed ones. Rejecting the
	// delimiter is the whole restriction: escaping it would put a second grammar
	// in the Helm template, where it could only ever drift from this parser.
	if strings.Contains(o.File, fieldSep) {
		return fmt.Errorf("observation source %q file %q must not contain %q, which separates fields on the controller's trace-source flag", o.Name, o.File, fieldSep)
	}
	switch {
	case o.ExistingClaim != "" && o.ConfigMap != "":
		return fmt.Errorf("observation source %q sets both existingClaim and configMap; exactly one backing is allowed", o.Name)
	case o.ExistingClaim == "" && o.ConfigMap == "":
		return fmt.Errorf("observation source %q must set exactly one of existingClaim or configMap", o.Name)
	}
	// The backing is a Kubernetes object name, and the Deployment references it by
	// name. Checking it here fails the controller's own configuration parsing with
	// a message naming the source, instead of shipping a Deployment the API server
	// rejects later for a reason nothing connects back to this value.
	if err := validateBackingName(o, "existingClaim", o.ExistingClaim); err != nil {
		return err
	}
	return validateBackingName(o, "configMap", o.ConfigMap)
}

// validateBackingName checks a non-empty backing reference is a legal Kubernetes
// object name. Also rejects the flag delimiter by construction: a DNS-1123
// subdomain has no comma.
func validateBackingName(o ObservationSource, field, value string) error {
	if value == "" {
		return nil
	}
	if errs := validation.IsDNS1123Subdomain(value); len(errs) > 0 {
		return fmt.Errorf("observation source %q %s %q is not a valid Kubernetes object name: %s", o.Name, field, value, strings.Join(errs, "; "))
	}
	return nil
}

// VolumeName is the managed volume/mount name for this source.
func (o ObservationSource) VolumeName() string { return observationVolumePrefix + o.Name }

// MountPath is the directory this source's backing is mounted at.
func (o ObservationSource) MountPath() string { return path.Join(ObservationMountRoot, o.Name) }

// FilePath is the absolute in-container path of the trace file.
//
// Because File is a single path segment, the file's parent directory IS
// [ObservationSource.MountPath]. That equality is load-bearing beyond this
// package: the dashboard is told only this path, and roots its read at the
// path's parent, so a nested file would silently move the read root below the
// mount and hand a symlink inside the volume a directory to escape through.
func (o ObservationSource) FilePath() string {
	return path.Join(o.MountPath(), o.File)
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
//
// The grammar is injective over everything [ObservationSource.Validate] accepts:
// no field may contain [fieldSep], and only the FIRST "=" of a field separates
// key from value, so a value may contain one. Chart values and this parser agree
// because the chart's schema rejects exactly what Validate rejects.
func ParseObservationSource(spec string) (ObservationSource, error) {
	var o ObservationSource
	for field := range strings.SplitSeq(spec, fieldSep) {
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
