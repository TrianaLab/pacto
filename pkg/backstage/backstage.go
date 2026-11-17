// Package backstage maps a fleet view onto Backstage catalog entities: a
// Component per service and a System per business domain. It is pure and
// k8s-free, and it only reads the [fleet.Query] it is handed, so the CLI file
// and any other caller mapping the same view produce the same bytes.
//
// An inferred value never fills a spec field a Backstage reader takes as fact
// (spec.owner, spec.system, spec.dependsOn and spec.consumesApis): it stays in
// an annotation or a tag. Every entity says how Pacto knows it, when and how
// completely.
package backstage

import (
	"cmp"
	"io"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Annotation keys Pacto writes on the entities it exports.
const (
	AnnotationProvenance      = "pacto.trianalab.io/provenance"
	AnnotationServiceKey      = "pacto.trianalab.io/service-key"
	AnnotationOwnerProvenance = "pacto.trianalab.io/owner-provenance"
	AnnotationOwnerConflict   = "pacto.trianalab.io/owner-conflict"
	AnnotationOwner           = "pacto.trianalab.io/owner"
	AnnotationOwnerUnmapped   = "pacto.trianalab.io/owner-unmapped"
	AnnotationAsOf            = "pacto.trianalab.io/as-of"
	AnnotationCompleteness    = "pacto.trianalab.io/completeness"
	AnnotationBusinessDomain  = "pacto.trianalab.io/business-domain"
	AnnotationNameProvenance  = "pacto.trianalab.io/name-provenance"
	AnnotationNameSource      = "pacto.trianalab.io/name-source"
)

// TagInferred marks an entity Pacto inferred, in a field the Backstage catalog
// can filter on (it neither shows nor filters annotations).
const TagInferred = "pacto-inferred"

const (
	apiVersion = "backstage.io/v1alpha1"
	unknown    = "unknown"
	kindSystem = "System"
	kindComp   = "Component"
)

// Entity is one Backstage catalog entity.
type Entity struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata is an entity's metadata block.
type Metadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace"`
	Title       string            `yaml:"title,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// Spec is an entity's spec block; each kind fills the fields it has.
type Spec struct {
	Type         string   `yaml:"type,omitempty"`
	Lifecycle    string   `yaml:"lifecycle,omitempty"`
	Owner        string   `yaml:"owner"`
	System       string   `yaml:"system,omitempty"`
	DependsOn    []string `yaml:"dependsOn,omitempty"`
	ProvidesAPIs []string `yaml:"providesApis,omitempty"`
	ConsumesAPIs []string `yaml:"consumesApis,omitempty"`
	Definition   string   `yaml:"definition,omitempty"`
}

// kindOrder is the order kinds are written in.
var kindOrder = map[string]int{kindSystem: 0, kindComp: 1}

// Map projects the view q onto Backstage entities, sorted by kind then name.
// It reads q's snapshot and never retains or changes it.
func Map(q *fleet.Query) []Entity {
	snap := q.SnapshotForSerialization()
	base := baseAnnotations{
		asOf:         q.ProductMeta().AsOf.UTC().Format(time.RFC3339),
		completeness: string(snap.Completeness),
	}
	services := exportedServices(snap)
	systemNames, out := systems(services, base)
	for i, name := range componentNames(services) {
		out = append(out, component(q, services[i], name, base, systemNames))
	}
	slices.SortFunc(out, func(a, b Entity) int {
		return cmp.Or(cmp.Compare(kindOrder[a.Kind], kindOrder[b.Kind]), cmp.Compare(a.Metadata.Name, b.Metadata.Name))
	})
	return out
}

// Encode writes es as a multi-document YAML stream: `---` between documents,
// a 2-space indent and a trailing newline.
func Encode(w io.Writer, es []Entity) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	for i := range es {
		if err := enc.Encode(&es[i]); err != nil {
			return err
		}
	}
	return enc.Close()
}

// baseAnnotations are the annotations every entity of one export shares.
type baseAnnotations struct {
	asOf, completeness string
}

// annotations starts an entity's annotations with its provenance, as-of and
// completeness.
func (b baseAnnotations) annotations(provenance string) map[string]string {
	return map[string]string{
		AnnotationProvenance:   provenance,
		AnnotationAsOf:         b.asOf,
		AnnotationCompleteness: b.completeness,
	}
}

// entity is a skeleton entity of kind in namespace default.
func entity(kind string, ann map[string]string) Entity {
	return Entity{APIVersion: apiVersion, Kind: kind, Metadata: Metadata{Namespace: "default", Annotations: ann}}
}

// exportedServices is every service of snap that runs or can run, sorted by
// key. A golden path runs nowhere and consumes nothing, so it is left out.
func exportedServices(snap *fleet.FleetSnapshot) []*fleet.ServiceRecord {
	var out []*fleet.ServiceRecord
	for _, s := range snap.Services {
		if !s.Template {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *fleet.ServiceRecord) int { return cmp.Compare(a.Key, b.Key) })
	return out
}
