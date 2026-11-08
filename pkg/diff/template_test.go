package diff

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func assertOneNonBreaking(t *testing.T, changes []Change, path string, ct ChangeType, reason string) {
	t.Helper()
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	c := changes[0]
	if c.Path != path || c.Type != ct || c.Classification != NonBreaking || !strings.Contains(c.Reason, reason) {
		t.Errorf("change = %+v, want %s %s NON_BREAKING with reason %q", c, path, ct, reason)
	}
}

func TestDiffTemplate(t *testing.T) {
	plugins := func(opt string) *contract.Template {
		return &contract.Template{Plugins: []contract.TemplatePlugin{
			{Name: "dockerfile"},
			{Name: "github-actions", Options: map[string]string{"go-version": opt}},
		}}
	}
	if got := diffTemplate(nil, nil); got != nil {
		t.Errorf("nil/nil = %+v", got)
	}
	if got := diffTemplate(plugins("1.27"), plugins("1.27")); got != nil {
		t.Errorf("unchanged = %+v", got)
	}
	assertOneNonBreaking(t, diffTemplate(nil, plugins("1.27")), "service.template", Added, "becomes a golden path")
	assertOneNonBreaking(t, diffTemplate(&contract.Template{}, nil), "service.template", Removed, "stops being a golden path")
	changes := diffTemplate(plugins("1.26"), plugins("1.27"))
	assertOneNonBreaking(t, changes, "service.template.plugins", Modified, "run only when a person asks")
	if got := changes[0].NewValue.([]string); strings.Join(got, ",") != "dockerfile,github-actions" {
		t.Errorf("new value = %v", got)
	}
}

func TestDiffOrigin(t *testing.T) {
	a := &contract.Origin{Ref: "oci://ghcr.io/acme/http-service:1.0.0", Digest: "sha256:" + strings.Repeat("a", 64)}
	b := &contract.Origin{Ref: a.Ref, Digest: "sha256:" + strings.Repeat("b", 64)}
	const reason = "declared provenance only"
	if got := diffOrigin(nil, nil); got != nil {
		t.Errorf("nil/nil = %+v", got)
	}
	if got := diffOrigin(a, &contract.Origin{Ref: a.Ref, Digest: a.Digest}); got != nil {
		t.Errorf("unchanged = %+v", got)
	}
	added := diffOrigin(nil, a)
	assertOneNonBreaking(t, added, "service.origin", Added, reason)
	if added[0].NewValue != a.Ref+"@"+a.Digest {
		t.Errorf("new value = %v", added[0].NewValue)
	}
	assertOneNonBreaking(t, diffOrigin(a, b), "service.origin", Modified, reason)
	assertOneNonBreaking(t, diffOrigin(a, nil), "service.origin", Removed, reason)
}
