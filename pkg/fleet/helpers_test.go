package fleet

import (
	"bytes"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// validLeafYAML is a leaf service that passes validation.Validate: structural
// schema, cross-field invariants (stateless+ephemeral), and empty-policy
// enforcement all succeed. Its openapi interface ref must exist in the bundle FS.
const validLeafYAML = `pactoVersion: '2.0'
service:
  name: leaf-svc
  version: 1.2.3
  owner:
    team: platform
    dri: alice
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
interfaces:
- name: http
  type: openapi
  ref: interfaces/openapi.json
  visibility: public
`

// invalidYAML parses into a structurally valid contract but violates the
// stateless+persistent cross-field invariant, so validation.Validate fails.
const invalidYAML = `pactoVersion: '2.0'
service:
  name: bad-svc
  version: 1.0.0
  owner:
    team: platform
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: persistent
  dataCriticality: low
`

// smallOpenAPI is a minimal valid OpenAPI 3.1 doc with two GET operations. The
// second declares only a description (no summary) to exercise the summary
// fallback in toolsFrom.
const smallOpenAPI = `{
  "openapi": "3.1.0",
  "info": {"title": "Leaf", "version": "1.0.0"},
  "paths": {
    "/a": {"get": {"operationId": "getA", "summary": "Get A", "responses": {"200": {"description": "ok"}}}},
    "/b": {"get": {"operationId": "getB", "description": "only a description", "responses": {"200": {"description": "ok"}}}}
  }
}`

// mustParse parses YAML into a contract or fails the test.
func mustParse(t *testing.T, y string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse(bytes.NewReader([]byte(y)))
	if err != nil {
		t.Fatalf("contract.Parse: %v", err)
	}
	return c
}

// validLeafBundle builds a fully valid bundle (RawYAML set, openapi file present)
// for the leaf service.
func validLeafBundle(t *testing.T) *contract.Bundle {
	t.Helper()
	fsys := fstest.MapFS{
		"interfaces/openapi.json": {Data: []byte(smallOpenAPI)},
	}
	return &contract.Bundle{
		Contract: mustParse(t, validLeafYAML),
		RawYAML:  []byte(validLeafYAML),
		FS:       fsys,
	}
}

// validLockYAML is a minimal current-schema lock for the "web" contract, naming
// dependency "dep-svc" and recording what the config scope "cfg-svc" actually
// resolved to. The reference digest is the ONLY authority a config/policy ref
// has: without it the scope name is just a label and the destination is unknown.
// The reference carries no `from`, i.e. web's own contract declared it.
const validLockYAML = `lockVersion: 2
pacto:
  version: 0.0.0
root:
  name: web
  version: 1.0.0
dependencies:
  - name: dep-svc
    source: oci
    ref: oci://example.com/dep-svc
    version: 2.0.0
    digest: sha256:deadbeef
references:
  - kind: config
    name: cfg-svc
    source: oci
    ref: oci://ex/cfg-svc
    version: 1.0.0
    digest: sha256:cfg
`

func mustLock(t *testing.T) *lock.Lock {
	t.Helper()
	l, err := lock.Parse([]byte(validLockYAML))
	if err != nil {
		t.Fatalf("lock.Parse: %v", err)
	}
	return l
}

// ptrTime returns a pointer to t.
func ptrTime(t time.Time) *time.Time { return &t }

// fixedNow is a deterministic clock for Build.
func fixedNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }
