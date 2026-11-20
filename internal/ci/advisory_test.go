package ci

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func TestCIPrintsInferredAdvisory(t *testing.T) {
	a := impact.InferredAdvisory{
		Advisory: diff.Advisory{Interface: "api", Kind: "openapi", Source: "openapi/v1.yaml:1",
			Declare: "interfaces:\n  - name: api\n    ref: \"a```b\"\n",
			Changes: []diff.Change{{Path: "openapi.parameters", Reason: "currency required"}, {Path: "x"}}},
		Consumers: []impact.AdvisoryConsumer{{Service: "web"}},
	}
	p := &Plan{Rows: []Row{{Service: "payments", Impact: Impact{Text: "unverified (possible break (inferred): interfaces[api]; declare it to classify)"},
		Notes: []string{"a note"}, Advisories: []impact.InferredAdvisory{a}}}}
	line := "possible break (inferred) payments api openapi/v1.yaml:1: openapi.parameters currency required (+1 more); consumers (inferred, unknown): web"
	var b strings.Builder
	Text(&b, p)
	if !strings.Contains(b.String(), "\n"+line+"\npayments: a note\n") {
		t.Errorf("text:\n%s", b.String())
	}
	md := Markdown(p)
	want := "\n- `" + line + "`\n\n  <details><summary>Declare api</summary>\n\n  ```yaml\n  interfaces:\n    - name: api\n      ref: \"a'''b\"\n  ```\n\n  </details>\n"
	if !strings.Contains(md, want) {
		t.Errorf("markdown:\n%s", md)
	}
	data, _ := json.Marshal(p)
	if !strings.Contains(string(data), `"advisories":[{"interface":"api"`) {
		t.Errorf("json: %s", data)
	}
}
