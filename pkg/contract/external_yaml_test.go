package contract_test

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const inferredStripeRef = "oci://registry.example.com/acme/pacto-inferred/stripe"

var externalDependencyYAMLRows = []struct {
	name string
	dep  contract.Dependency
	want string
}{
	{"ref-less with version", contract.Dependency{Name: "stripe", External: true, Required: true, Version: "2023-10-16"},
		"dependencies:\n  - name: stripe\n    external: true\n    required: true\n    version: 2023-10-16\n"},
	{"ref-less with identity", contract.Dependency{Name: "legacy", External: true, Identity: "host/corp-legacy.example", Required: true, Version: "v1"},
		"dependencies:\n  - name: legacy\n    external: true\n    identity: host/corp-legacy.example\n    required: true\n    version: v1\n"},
	{"ref-less optional", contract.Dependency{Name: "stripe", External: true, Version: "v1"},
		"dependencies:\n  - name: stripe\n    external: true\n    required: false\n    version: v1\n"},
	{"ref-less drops compatibility", contract.Dependency{Name: "stripe", External: true, Required: true, Version: "v1", Compatibility: "^1.0.0"},
		"dependencies:\n  - name: stripe\n    external: true\n    required: true\n    version: v1\n"},
	{"ref with compatibility", contract.Dependency{Name: "stripe", Ref: inferredStripeRef, Required: true, Compatibility: "^2023.10.0"},
		"dependencies:\n  - name: stripe\n    ref: " + inferredStripeRef + "\n    required: true\n    compatibility: \"^2023.10.0\"\n"},
	{"ref with unknown", contract.Dependency{Name: "stripe", Ref: inferredStripeRef, Required: true, Compatibility: contract.CompatibilityUnknown, External: true, Identity: "stripe", Version: "v1"},
		"dependencies:\n  - name: stripe\n    ref: " + inferredStripeRef + "\n    required: true\n    compatibility: \"unknown\"\n"},
}

func TestExternalDependencyYAML(t *testing.T) {
	for _, row := range externalDependencyYAMLRows {
		if got := contract.ExternalDependencyYAML(row.dep); got != row.want {
			t.Errorf("%s:\n%s\nwant:\n%s", row.name, got, row.want)
		}
	}
}

func TestExternalDependencyYAMLParses(t *testing.T) {
	for _, row := range externalDependencyYAMLRows {
		raw := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: 1.0.0\n" + contract.ExternalDependencyYAML(row.dep)
		c, err := contract.Parse(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		if r := validation.Validate(c, []byte(raw), nil); !r.IsValid() {
			t.Errorf("%s: %v\n%s", row.name, r.Errors, raw)
		}
	}
}

func TestExternalDependencyName(t *testing.T) {
	for id, want := range map[string]string{"stripe": "stripe", "host/corp.example": "corp-example", "aws/rds/123456789012/eu-west-1/orders-db": "orders-db"} {
		if got := contract.ExternalDependencyName(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}
