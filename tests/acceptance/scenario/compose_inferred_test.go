package scenario

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// TestComposeInferredService: the demo publishes stripe's inferred contract
// with the installation key it mints, over the seeded bundles and the
// Compose-only consumer, and is healthy once a publish succeeded.
func TestComposeInferredService(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	svc := composeSvc(t, f, "inferred")
	if svc["image"] != composeOpts.PactoImage {
		t.Errorf("inferred runs %v, want the pacto image", svc["image"])
	}
	script := argsOf(t, svc, "command")
	for _, want := range []string{
		"pacto evidence keygen --out " + composeInferredDir + " --key-id " + composeInferredKeyID,
		"pacto fleet publish-inferred --registry " + ComposeDomain + " --key " + composeInferredKey + " --local " + ComposeInferredContractDir + " --oci oci://" + ComposeDomain + "/",
		"touch " + composeInferredPublished,
		"sleep 300",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the inferred script lacks %q:\n%s", want, script)
		}
	}
	hc, _ := svc["healthcheck"].(map[string]any)
	if test := argsOf(t, hc, "test"); !strings.Contains(test, composeInferredPublished) {
		t.Errorf("inferred is healthy on %q; want the marker its first publish writes", test)
	}
	deps, _ := svc["depends_on"].(map[string]any)
	if _, ok := deps["seed"]; !ok {
		t.Error("inferred does not wait for the seeded bundles it reads")
	}
	if _, taken := composeReservedServices["inferred"]; !taken {
		t.Error("a workload could take the inferred service name")
	}
	if got := inlineConfig(t, f, composeInferredInput.name); got != composeInferredContract {
		t.Errorf("the Compose-only contract = %q", got)
	}
}

// TestComposeInferredContractPinsTheInferredContract: the Compose-only
// contract is valid and pins stripe's inferred contract, the edge the demo
// asserts reads unknown with INFERRED_PROVIDER.
func TestComposeInferredContractPinsTheInferredContract(t *testing.T) {
	c, err := contract.Parse(strings.NewReader(composeInferredContract))
	if err != nil {
		t.Fatal(err)
	}
	if res := validation.Validate(c, []byte(composeInferredContract), fstest.MapFS{}); !res.IsValid() {
		t.Fatalf("the Compose-only contract is invalid: %+v", res.Errors)
	}
	var pinned bool
	for _, d := range c.Dependencies {
		if id, ok := contract.InferredRef(d.Ref); ok && id == "stripe" && d.Compatibility != "" {
			pinned = true
		}
	}
	if !pinned {
		t.Error("no dependency pins stripe's inferred contract")
	}
}
