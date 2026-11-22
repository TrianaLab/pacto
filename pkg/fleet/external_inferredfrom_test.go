package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// TestFleetInferredFromPatternInStep keeps the record's external:
// inferredFrom in step with the fleet's external subject and externalid's
// identities.
func TestFleetInferredFromPatternInStep(t *testing.T) {
	for _, s := range []string{"stripe", "host/corp-legacy.example", "aws/rds/123456789012/eu-west-1/orders-db", "aws/rds"} {
		id, err := externalid.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		from := contract.ExternalInferredFrom(id.String())
		if from != ExternalSubjectPrefix+s {
			t.Errorf("ExternalInferredFrom(%q) = %q, not the fleet subject", s, from)
		}
		if got, ok := contract.InferredFromExternal(from); !ok || got != s {
			t.Errorf("InferredFromExternal(%q) = %q %v", from, got, ok)
		}
	}
}
