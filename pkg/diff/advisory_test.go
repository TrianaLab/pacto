package diff

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const usersV1 = `openapi: "3.0.0"
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

// usersV2 drops the required response field email.
const usersV2 = `openapi: "3.0.0"
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
                required: [id]
                properties:
                  id: {type: string}
`

// usersV1Described changes only a description: no breaking finding.
const usersV1Described = `openapi: "3.0.0"
paths:
  /users:
    get:
      summary: list users
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

// specField is the inferred interfaces[name] entry for a kind spec at path.
func specField(name, kind, path string) contract.Inferred {
	f := field("interfaces["+name+"]", kind+" "+path, path+":1")
	f.Declare = "interfaces:\n  - name: " + name + "\n    type: " + kind + "\n    ref: " + path + "\n"
	return f
}

// inferredSpecFS is a bundle FS whose record infers the spec at path and
// holds it, when spec is not empty.
func inferredSpecFS(name, kind, path, spec string) fstest.MapFS {
	fsys := recordFS("a", specField(name, kind, path))
	if spec != "" {
		fsys[path] = &fstest.MapFile{Data: []byte(spec)}
	}
	return fsys
}

func TestInferredOpenAPIRemovedRequiredFieldIsAdvisory(t *testing.T) {
	c := bareContract()
	r := Compare(context.Background(), c, c,
		inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1), inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV2))
	if r.Classification != NonBreaking {
		t.Errorf("classification %s, want NON_BREAKING", r.Classification)
	}
	for _, ch := range r.Changes {
		if ch.Classification == Breaking {
			t.Errorf("a BREAKING change in changes: %+v", ch)
		}
	}
	if len(r.Advisories) != 1 {
		t.Fatalf("advisories %+v", r.Advisories)
	}
	a := r.Advisories[0]
	if a.Interface != "api" || a.Kind != "openapi" || a.Source != "openapi/v1.yaml:1" || !strings.Contains(a.Declare, "name: api") {
		t.Errorf("advisory %+v", a)
	}
	if len(a.Changes) == 0 || !strings.Contains(a.Changes[0].Path, "responses[200]") {
		t.Errorf("findings %+v", a.Changes)
	}
	if s := a.Summary(); !strings.HasPrefix(s, "api openapi/v1.yaml:1: "+a.Changes[0].Path+" ") {
		t.Errorf("summary %q", s)
	}
}

func TestDeclaredInterfaceStillBreaking(t *testing.T) {
	c := bareContract()
	c.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "openapi/v1.yaml"}}
	r := Compare(context.Background(), c, c,
		inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1), inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV2))
	if r.Classification != Breaking || len(r.Advisories) != 0 {
		t.Errorf("classification %s, advisories %+v", r.Classification, r.Advisories)
	}
}

func TestInferredAsyncAPIAndGRPCAdvisory(t *testing.T) {
	c := bareContract()
	asyncNew := strings.Split(baseAsyncAPI, "  order.cancelled:")[0]
	protoNew := strings.Replace(baseProto, "  rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);\n", "", 1)
	for _, tc := range []struct{ name, kind, path, old, new string }{
		{"events", "asyncapi", "asyncapi.yaml", baseAsyncAPI, asyncNew},
		{"grpc", "grpc", "api.proto", baseProto, protoNew},
	} {
		r := Compare(context.Background(), c, c, inferredSpecFS(tc.name, tc.kind, tc.path, tc.old), inferredSpecFS(tc.name, tc.kind, tc.path, tc.new))
		if r.Classification != NonBreaking || len(r.Advisories) != 1 || r.Advisories[0].Interface != tc.name || r.Advisories[0].Kind != tc.kind {
			t.Errorf("%s: classification %s, advisories %+v", tc.kind, r.Classification, r.Advisories)
		}
	}
}

func TestInferredAdvisoryOnlyBreakingFindings(t *testing.T) {
	c := bareContract()
	r := Compare(context.Background(), c, c,
		inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1), inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV2))
	for _, ch := range r.Advisories[0].Changes {
		if ch.Classification != Breaking {
			t.Errorf("a finding that is not breaking: %+v", ch)
		}
	}
	r = Compare(context.Background(), c, c,
		inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1), inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1Described))
	if len(r.Advisories) != 0 {
		t.Errorf("a change with no breaking finding gave %+v", r.Advisories)
	}
}

func TestInferredAdvisoryNeedsBaselineRecordAndFile(t *testing.T) {
	c := bareContract()
	newFS := inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV2)
	for name, oldFS := range map[string]fstest.MapFS{
		"no old record":   {"openapi/v1.yaml": {Data: []byte(usersV1)}},
		"missing file":    inferredSpecFS("api", "openapi", "openapi/v1.yaml", ""),
		"unreadable file": inferredSpecFS("api", "openapi", "openapi/v1.yaml", "paths: [unclosed"),
		"kind change":     inferredSpecFS("api", "asyncapi", "openapi/v1.yaml", usersV1),
	} {
		if r := Compare(context.Background(), c, c, oldFS, newFS); len(r.Advisories) != 0 {
			t.Errorf("%s: advisories %+v", name, r.Advisories)
		}
	}
	if r := Compare(context.Background(), c, c, inferredSpecFS("api", "openapi", "openapi/v1.yaml", usersV1), nil); len(r.Advisories) != 0 {
		t.Errorf("no new bundle: advisories %+v", r.Advisories)
	}
}

func TestInferredAdvisorySummaryCountsMore(t *testing.T) {
	a := Advisory{Interface: "api", Source: "a.yaml:1", Changes: []Change{{Path: "p", Reason: "r"}, {Path: "q"}, {Path: "s"}}}
	if got := a.Summary(); got != "api a.yaml:1: p r (+2 more)" {
		t.Errorf("summary %q", got)
	}
	if got := (Advisory{Interface: "api", Source: "a.yaml:1"}).Summary(); got != "api a.yaml:1" {
		t.Errorf("summary %q", got)
	}
}

func TestSpecDiffOtherKindIsNothing(t *testing.T) {
	if got := specDiff("graphql", "a", "b", fstest.MapFS{}, fstest.MapFS{}); got != nil {
		t.Errorf("specDiff %+v", got)
	}
}
