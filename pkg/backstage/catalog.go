package backstage

import (
	"encoding/json"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// SourceKind is the kind, and the default id, of the source that reads a
// Backstage catalog's components as inferred service facts.
const SourceKind = "backstage-catalog"

// MaxCatalogEntities is how many components one read of a catalog keeps; the
// read stops there and says so.
const MaxCatalogEntities = 5000

// Bounds on what one catalog entity may carry into a fact.
const (
	maxFactRunes     = 256
	maxFactDependsOn = 50
)

// CatalogPage is one page of the catalog API's entities/by-query answer. The
// decode is lenient: unknown fields are ignored.
type CatalogPage struct {
	Items    []catalogEntity `json:"items"`
	PageInfo struct {
		NextCursor string `json:"nextCursor"`
	} `json:"pageInfo"`
}

// catalogEntity is the part of a catalog entity a fact reads.
type catalogEntity struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Owner     string   `json:"owner"`
		System    string   `json:"system"`
		DependsOn []string `json:"dependsOn"`
	} `json:"spec"`
}

// ReadCatalogPage decodes one entities/by-query page into a fact per
// Component, read at at, and returns the cursor of the next page ("" for the
// last one). An owner, system or depends-on the contract rules do not accept
// is left out; only component refs count as dependencies. A Component Pacto
// exported is marked so [fleet.Build] never reads it back.
func ReadCatalogPage(data []byte, at time.Time) (facts []fleet.RawServiceFact, next string, err error) {
	var page CatalogPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, "", err
	}
	for _, e := range page.Items {
		if strings.EqualFold(e.Kind, kindComp) && cleanFact(e.Metadata.Name) != "" {
			facts = append(facts, entityFact(e, at))
		}
	}
	return facts, page.PageInfo.NextCursor, nil
}

// entityFact is the fact of the Component e.
func entityFact(e catalogEntity, at time.Time) fleet.RawServiceFact {
	name := cleanFact(e.Metadata.Name)
	ns := cleanFact(e.Metadata.Namespace)
	if ns == "" {
		ns = "default"
	}
	ann := e.Metadata.Annotations
	f := fleet.RawServiceFact{
		Name:       name,
		ServiceKey: fleet.ServiceKey(cleanFact(ann[AnnotationServiceKey])),
		Entity:     "component:" + ns + "/" + name,
		Exported:   ann[AnnotationProvenance] != "",
		ObservedAt: &at,
	}
	if o, ok := OwnerFromRef(cleanFact(e.Spec.Owner)); ok {
		f.Owner = o
	}
	if d, ok := DomainFromRef(cleanFact(e.Spec.System)); ok {
		f.BusinessDomain = d
	}
	for _, ref := range e.Spec.DependsOn {
		if r, ok := dependsOnRef(ref); ok && len(f.DependsOn) < maxFactDependsOn {
			f.DependsOn = append(f.DependsOn, r)
		}
	}
	return f
}

// dependsOnRef is the depends-on ref when it names a component.
func dependsOnRef(ref string) (fleet.RawFactRef, bool) {
	ref = cleanFact(ref)
	kind, name := EntityRef(ref)
	if kind != "component" || name == "" {
		return fleet.RawFactRef{}, false
	}
	ns, base := "default", path.Base(name)
	if dir := path.Dir(name); dir != "." {
		ns = dir
	}
	return fleet.RawFactRef{Name: base, Entity: "component:" + ns + "/" + base}, true
}

// cleanFact drops C0 and C1 control characters and the Unicode bidi controls
// from s, then keeps its first maxFactRunes runes.
func cleanFact(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (r >= 0x200E && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxFactRunes {
		return string(r[:maxFactRunes])
	}
	return s
}
