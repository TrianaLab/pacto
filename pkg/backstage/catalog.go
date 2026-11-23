package backstage

import (
	"encoding/json"
	"net/url"
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
	maxFactRunes = 256
	// MaxFactDependsOn is how many component dependencies one fact keeps; a
	// read that trims more says so.
	MaxFactDependsOn = 50
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
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Owner     string   `json:"owner"`
		System    string   `json:"system"`
		Type      string   `json:"type"`
		DependsOn []string `json:"dependsOn"`
	} `json:"spec"`
}

// kindTemplate is the kind of a scaffolder software template.
const kindTemplate = "Template"

// scaffolderGroup prefixes the apiVersion of a scaffolder template.
const scaffolderGroup = "scaffolder.backstage.io/"

// CatalogRead is what one entities/by-query page holds.
type CatalogRead struct {
	// Facts holds a fact per Component.
	Facts []fleet.RawServiceFact
	// Templates holds an inferred golden path per scaffolder Template, with
	// no From or URL: the source sets them.
	Templates []fleet.RawInferredTemplate
	// Next is the cursor of the next page, "" for the last one.
	Next string
	// Trimmed is how many Components listed more than [MaxFactDependsOn]
	// component dependencies, which were trimmed.
	Trimmed int
}

// ReadCatalogPage decodes one entities/by-query page, read at at, into a
// fact per Component and an inferred golden path per scaffolder Template. An
// owner, system or depends-on the contract rules do not accept is left out;
// only component refs count as dependencies. A Component Pacto exported is
// marked so [fleet.Build] never reads it back; a Template Pacto exported is
// skipped.
func ReadCatalogPage(data []byte, at time.Time) (CatalogRead, error) {
	var page CatalogPage
	if err := json.Unmarshal(data, &page); err != nil {
		return CatalogRead{}, err
	}
	out := CatalogRead{Next: page.PageInfo.NextCursor}
	for _, e := range page.Items {
		if cleanFact(e.Metadata.Name) == "" {
			continue
		}
		switch {
		case strings.EqualFold(e.Kind, kindComp):
			f, cut := entityFact(e, at)
			out.Facts = append(out.Facts, f)
			if cut {
				out.Trimmed++
			}
		case strings.EqualFold(e.Kind, kindTemplate) && strings.HasPrefix(e.APIVersion, scaffolderGroup) &&
			e.Metadata.Annotations[AnnotationProvenance] == "":
			out.Templates = append(out.Templates, templateEntity(e, at))
		}
	}
	return out, nil
}

// templateEntity is the inferred golden path of the scaffolder Template e.
// Its name is kept as read: the fold drops a name a contract cannot carry.
func templateEntity(e catalogEntity, at time.Time) fleet.RawInferredTemplate {
	name := cleanFact(e.Metadata.Name)
	ns := cleanFact(e.Metadata.Namespace)
	if ns == "" {
		ns = "default"
	}
	t := fleet.RawInferredTemplate{
		Name:       name,
		Entity:     "template:" + ns + "/" + name,
		Title:      cleanFact(e.Metadata.Title),
		Summary:    cleanFact(e.Metadata.Description),
		Type:       cleanFact(e.Spec.Type),
		ObservedAt: &at,
	}
	if t.Summary == "" {
		t.Summary = t.Title
	}
	if o, ok := OwnerFromRef(cleanFact(e.Spec.Owner)); ok {
		t.Owner = o
	}
	return t
}

// ScaffolderRoot is the scaffolder page of the Backstage at base.
func ScaffolderRoot(base string) string { return strings.TrimRight(base, "/") + "/create" }

// ScaffolderURL is the scaffolder page of one template of the Backstage at
// base, "" when namespace or name is empty or a dot segment, which a browser
// would resolve away even escaped. Both are path-escaped, so neither can
// shape the link.
//
// ponytail: the API URL is assumed to be the URL people open Backstage at;
// add a separate app URL value when a catalog serves its API elsewhere.
func ScaffolderURL(base, namespace, name string) string {
	for _, seg := range []string{namespace, name} {
		if seg == "" || seg == "." || seg == ".." {
			return ""
		}
	}
	return ScaffolderRoot(base) + "/templates/" + url.PathEscape(namespace) + "/" + url.PathEscape(name)
}

// entityFact is the fact of the Component e, and whether its component
// dependencies were trimmed to [MaxFactDependsOn].
func entityFact(e catalogEntity, at time.Time) (fleet.RawServiceFact, bool) {
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
	cut := false
	for _, ref := range e.Spec.DependsOn {
		r, ok := dependsOnRef(ref)
		switch {
		case !ok:
		case len(f.DependsOn) < MaxFactDependsOn:
			f.DependsOn = append(f.DependsOn, r)
		default:
			cut = true
		}
	}
	return f, cut
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
