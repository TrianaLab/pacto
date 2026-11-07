package validation

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestValidateTemplate(t *testing.T) {
	var r ValidationResult
	validateTemplate(&contract.Contract{}, &r)
	if len(r.Errors) != 0 {
		t.Fatalf("nil template: %v", r.Errors)
	}
	c := &contract.Contract{Service: contract.Service{Template: &contract.Template{Plugins: []contract.TemplatePlugin{{Name: "dockerfile"}, {Name: "dockerfile"}, {Name: "ci"}}}}}
	validateTemplate(c, &r)
	if len(r.Errors) != 1 || r.Errors[0].Code != "DUPLICATE_TEMPLATE_PLUGIN" || r.Errors[0].Path != "service.template.plugins[1].name" {
		t.Fatalf("duplicate: %v", r.Errors)
	}
}

func TestValidateOrigin(t *testing.T) {
	for ref, wantErr := range map[string]bool{
		"oci://ghcr.io/a/b:1.0.0":                             false,
		"oci://ghcr.io/a/b@sha256:" + strings.Repeat("a", 64): false,
		"../paths/x":  false,
		"oci://":      true,
		"https://x/y": true,
	} {
		var r ValidationResult
		validateOrigin(&contract.Contract{Service: contract.Service{Origin: &contract.Origin{Ref: ref}}}, &r)
		if got := len(r.Errors) == 1 && r.Errors[0].Code == "INVALID_ORIGIN_REF" && r.Errors[0].Path == "service.origin.ref"; got != wantErr || (!wantErr && len(r.Errors) != 0) {
			t.Errorf("%q: %v", ref, r.Errors)
		}
	}
	var r ValidationResult
	validateOrigin(&contract.Contract{}, &r)
	if len(r.Errors) != 0 {
		t.Fatalf("nil origin: %v", r.Errors)
	}
}
