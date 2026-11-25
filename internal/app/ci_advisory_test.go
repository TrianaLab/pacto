package app

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// usersSpec is an OpenAPI file whose GET /users response requires email.
const usersSpec = `openapi: 3.0.0
info: {title: reports, version: "1"}
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, email]
                properties:
                  id: {type: string}
                  email: {type: string}
`

// usersSpecDropped drops the required response field email.
var usersSpecDropped = strings.Replace(strings.Replace(usersSpec, "[id, email]", "[id]", 1), "                  email: {type: string}\n", "", 1)

// droppedResponseField publishes reports 1.0.0 with the spec, declared in
// pacto.yaml or only inferred, and drops a required response field from it.
func (e *ciRepoEnv) droppedResponseField(t *testing.T, declared string) {
	t.Helper()
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1 + declared, "reports/openapi.yaml": usersSpec})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n" + declared, "reports/openapi.yaml": usersSpecDropped})
}

func TestCIInferredPossibleBreakStaysUnverified(t *testing.T) {
	t.Parallel()
	e := newCIRepo(t)
	e.droppedResponseField(t, "")
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}, Index: IndexNone})
	r := row(t, p, "reports")
	if r.Classification != "NON_BREAKING" || r.Impact.Gate != impact.GateUnverified || p.Exit != 2 ||
		r.Impact.Text != "unverified (possible break (inferred): interfaces[api]; declare it to classify)" {
		t.Fatalf("row = %+v, exit %d", r, p.Exit)
	}
	if len(r.Advisories) != 1 || r.Advisories[0].Interface != "api" || res["reports"].GateCode() != impact.GateCodeInferredPossibleBreak {
		t.Fatalf("advisories %+v, code %s", r.Advisories, res["reports"].GateCode())
	}
}

func TestCIDeclaredInterfaceBreakIsBreaking(t *testing.T) {
	t.Parallel()
	e := newCIRepo(t)
	e.droppedResponseField(t, interfaceYAML)
	p, _ := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}, Index: IndexNone})
	if r := row(t, p, "reports"); r.Classification != "BREAKING" || len(r.Advisories) != 0 || r.Next != "2.0.0" {
		t.Fatalf("row = %+v", r)
	}
}

func TestPublishBumpIgnoresInferredAdvisory(t *testing.T) {
	t.Parallel()
	e := newCIRepo(t)
	e.droppedResponseField(t, "")
	p, _ := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}, Index: IndexNone})
	// The derived version is the bump publish enforces: a patch, the
	// advisory never raises it.
	if r := row(t, p, "reports"); r.Next != "1.0.1" || !r.Derived {
		t.Fatalf("row = %+v", r)
	}
}
