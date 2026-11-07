package diff

import (
	"maps"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffTemplate compares service.template (2.7). Marking or unmarking a golden
// path and changing the plugins it names change nothing a consumer calls.
func diffTemplate(o, n *contract.Template) []Change {
	switch {
	case o == nil && n == nil:
		return nil
	case o == nil:
		c := newChange("service.template", Added, nil, pluginNames(n))
		c.Reason = "the contract becomes a golden path: services are created from it, and nothing a consumer calls changes"
		return []Change{c}
	case n == nil:
		c := newChange("service.template", Removed, pluginNames(o), nil)
		c.Reason = "the contract stops being a golden path; services created from it keep their own contracts"
		return []Change{c}
	}
	if slices.EqualFunc(o.Plugins, n.Plugins, func(a, b contract.TemplatePlugin) bool {
		return a.Name == b.Name && a.Description == b.Description && maps.Equal(a.Options, b.Options)
	}) {
		return nil
	}
	c := newChange("service.template.plugins", Modified, pluginNames(o), pluginNames(n))
	c.Reason = "the plugins the golden path names changed; they run only when a person asks at creation"
	return []Change{c}
}

func pluginNames(t *contract.Template) []string {
	names := make([]string, 0, len(t.Plugins))
	for _, p := range t.Plugins {
		names = append(names, p.Name)
	}
	return names
}

// diffOrigin compares service.origin (2.7), declared provenance only.
func diffOrigin(o, n *contract.Origin) []Change {
	if o == nil && n == nil || o != nil && n != nil && *o == *n {
		return nil
	}
	var oldVal, newVal any
	if o != nil {
		oldVal = o.Ref + "@" + o.Digest
	}
	if n != nil {
		newVal = n.Ref + "@" + n.Digest
	}
	ct := Modified
	switch {
	case o == nil:
		ct = Added
	case n == nil:
		ct = Removed
	}
	c := newChange("service.origin", ct, oldVal, newVal)
	c.Reason = "declared provenance only: where the service was created from; nothing a consumer calls changes"
	return []Change{c}
}
