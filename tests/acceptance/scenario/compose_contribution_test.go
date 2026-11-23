package scenario

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// TestComposeContributionInputs: the seed carries compose-idp's record bundle
// (a name and a version in pacto.yaml, a verifiable record naming the deaf
// workload inventory, whose evidence targets no other check counts) and its
// WorkloadObserved report on that subject, and is told where they are and who
// contributes.
func TestComposeContributionInputs(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	subjects := composeEvidenceSubjects(t, OperationalGraph)
	files := fstest.MapFS{}
	for _, rel := range []string{"pacto.yaml", composeContributionExport, contract.InferredRecordFile} {
		files[rel] = &fstest.MapFile{Data: []byte(inlineConfig(t, f, "contribution_bundle_"+rel))}
	}
	c, err := contract.Parse(bytes.NewReader(files["pacto.yaml"].Data))
	if err != nil || c.Service.Name != ComposeContributedService || c.Service.Version == "" {
		t.Fatalf("the contributed pacto.yaml = %+v, %v", c, err)
	}
	if res := validation.Validate(c, files["pacto.yaml"].Data, files); !res.IsValid() {
		t.Errorf("the contributed pacto.yaml is invalid: %+v", res.Errors)
	}
	rec, err := contract.ReadInferredRecord(files)
	if err == nil {
		err = contract.VerifyInferredRecord(files, rec)
	}
	calls := "inventory"
	if err != nil || len(rec.Fields) != 1 || rec.Fields[0].Field != "dependencies["+calls+"]" {
		t.Fatalf("the record = %+v, %v", rec, err)
	}

	var set evidence.EvidenceSet
	if err := json.Unmarshal([]byte(inlineConfig(t, f, "contribution_workload.json")), &set); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(subjects, set.ContractRef) || !strings.Contains(set.ContractRef, "/inventory@") || set.Subject.Name != "inventory-idp" ||
		len(set.Observations) != 1 || set.Observations[0].Kind != evidence.WorkloadObserved {
		t.Errorf("the report = %+v", set)
	}
	if got := inlineConfig(t, f, "contribution_workload.json"); !strings.Contains(got, composeContributionStamp) {
		t.Errorf("the report carries no %s for the seed to stamp:\n%s", composeContributionStamp, got)
	}

	env, _ := composeSvc(t, f, "seed")["environment"].(map[string]any)
	if env["PACTO_DEMO_CONTRIBUTION"] != ComposeContributionDir || env["PACTO_DEMO_CONTRIBUTOR"] != ComposeContributor ||
		env["PACTO_DEMO_CONTRIBUTION_STAMP"] != composeContributionStamp {
		t.Errorf("the seed environment = %v", env)
	}
	if !strings.Contains(seedScript, `--sign-key "$key"`) || !strings.Contains(seedScript, "pacto-inferred/$PACTO_DEMO_CONTRIBUTOR/services") {
		t.Error("the seed does not push and sign the contribution")
	}
}

// TestComposeContributionRefusesWhatItCannotReport: no evidence subject, a
// subject no service is published at, or a contract already named like the
// contributed service, is refused.
func TestComposeContributionRefusesWhatItCannotReport(t *testing.T) {
	s := OperationalGraph
	if _, _, err := s.composeContribution(nil); err == nil {
		t.Error("a contribution with no subject to report on")
	}
	if _, _, err := s.composeContribution([]string{"oci://" + ComposeDomain + "/nowhere@sha256:" + strings.Repeat("0", 64)}); err == nil {
		t.Error("a contribution on a subject no service is published at")
	}
	s.Services = append(append([]Service{}, s.Services...), Service{Name: ComposeContributedService})
	if _, _, err := s.composeContribution([]string{"oci://" + ComposeDomain + "/inventory@sha256:" + strings.Repeat("0", 64)}); err == nil {
		t.Error("a contribution a contract would supersede")
	}
}
