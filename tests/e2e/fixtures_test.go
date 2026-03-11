//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Contract YAML templates for test fixtures.

const openapiTemplate = `openapi: "3.0.0"
info:
  title: %s
  version: %s
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
`

const openapiWithMethodsV1 = `openapi: "3.0.0"
info:
  title: user-api
  version: "1.0.0"
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
  /users:
    get:
      summary: List users
      parameters:
        - name: sort
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    name:
                      type: string
    delete:
      summary: Delete all users
      responses:
        "204":
          description: Deleted
`

const openapiWithMethodsV2 = `openapi: "3.0.0"
info:
  title: user-api
  version: "2.0.0"
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
  /users:
    get:
      summary: List users
      parameters:
        - name: filter
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    name:
                      type: string
                    email:
                      type: string
        "404":
          description: Not Found
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        "201":
          description: Created
  /orders:
    get:
      summary: List orders
      responses:
        "200":
          description: OK
`

const protoTemplate = `syntax = "proto3";
package %s;

service %sService {
  rpc Health (HealthRequest) returns (HealthResponse);
}

message HealthRequest {}
message HealthResponse {
  string status = 1;
}
`

const configSchemaTemplate = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
`

func myAppContractV1(registryHost string) string {
	return fmt.Sprintf(`pactoVersion: "1.0"

service:
  name: my-app
  version: 1.0.0
  owner: team/platform

interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml

configuration:
  schema: configuration/schema.json

dependencies:
  - ref: oci://%s/postgres-pacto:1.0.0
    required: true
    compatibility: "^1.0.0"
  - ref: oci://%s/redis-pacto:1.0.0
    required: false
    compatibility: "^1.0.0"

runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30
  health:
    interface: api
    path: /health
    initialDelaySeconds: 5

scaling:
  min: 1
  max: 5

metadata:
  team: platform
  tier: standard
`, registryHost, registryHost)
}

const postgresContractV1 = `pactoVersion: "1.0"

service:
  name: postgres-pacto
  version: 1.0.0
  owner: team/data

interfaces:
  - name: db
    type: grpc
    port: 5432
    visibility: internal
    contract: interfaces/db.proto

configuration:
  schema: configuration/schema.json

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: shared
      durability: persistent
    dataCriticality: high
  health:
    interface: db
    path: /health

scaling:
  min: 1
  max: 3

metadata:
  team: data
  tier: critical
`

const redisContractV1 = `pactoVersion: "1.0"

service:
  name: redis-pacto
  version: 1.0.0
  owner: team/data

interfaces:
  - name: cache
    type: grpc
    port: 6379
    visibility: internal
    contract: interfaces/cache.proto

configuration:
  schema: configuration/schema.json

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: shared
      durability: persistent
    dataCriticality: medium
  health:
    interface: cache
    path: /health

scaling:
  min: 1
  max: 3

metadata:
  team: data
  tier: standard
`

const redisContractV2 = `pactoVersion: "1.0"

service:
  name: redis-pacto
  version: 2.0.0
  owner: team/data

interfaces:
  - name: cache
    type: grpc
    port: 6379
    visibility: internal
    contract: interfaces/cache.proto

configuration:
  schema: configuration/schema.json

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: shared
      durability: persistent
    dataCriticality: high
  health:
    interface: cache
    path: /health

scaling:
  min: 2
  max: 6

metadata:
  team: data
  tier: critical
`

const brokenContract = `pactoVersion: "1.0"
service:
  name: broken-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: wrong-name
    path: /health
`

// myAppContractV2 references redis v2 (upgraded) and drops postgres (removed).
func myAppContractV2(registryHost string) string {
	return fmt.Sprintf(`pactoVersion: "1.0"

service:
  name: my-app
  version: 2.0.0
  owner: team/platform

interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml

configuration:
  schema: configuration/schema.json

dependencies:
  - ref: oci://%s/redis-pacto:2.0.0
    required: true
    compatibility: "^2.0.0"

runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30
  health:
    interface: api
    path: /health

scaling:
  min: 1
  max: 5

metadata:
  team: platform
  tier: standard
`, registryHost)
}

// writeMyAppV2Bundle creates the my-app@2.0.0 bundle directory.
func writeMyAppV2Bundle(t *testing.T, registryHost string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my-app-v2")
	return writeBundleDir(t, dir, myAppContractV2(registryHost), map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "my-app", "2.0.0"),
	})
}

// writeBundleDir writes a contract YAML and companion files to a directory.
// Returns the directory path.
func writeBundleDir(t *testing.T, dir, contractYAML string, ifaceFiles map[string]string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "configuration"), 0755); err != nil {
		t.Fatal(err)
	}

	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := os.WriteFile(pactoPath, []byte(contractYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Write interface files
	for name, content := range ifaceFiles {
		p := filepath.Join(dir, "interfaces", name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Write config schema
	schemaPath := filepath.Join(dir, "configuration", "schema.json")
	if err := os.WriteFile(schemaPath, []byte(configSchemaTemplate), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

// writeMyAppV1Bundle creates the my-app@1.0.0 bundle directory.
func writeMyAppV1Bundle(t *testing.T, registryHost string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my-app-v1")
	return writeBundleDir(t, dir, myAppContractV1(registryHost), map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "my-app", "1.0.0"),
	})
}

// writePostgresBundle creates the postgres-pacto@1.0.0 bundle directory.
func writePostgresBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "postgres-pacto")
	return writeBundleDir(t, dir, postgresContractV1, map[string]string{
		"db.proto": fmt.Sprintf(protoTemplate, "postgres", "Postgres"),
	})
}

// writeRedisV1Bundle creates the redis-pacto@1.0.0 bundle directory.
func writeRedisV1Bundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "redis-pacto-v1")
	return writeBundleDir(t, dir, redisContractV1, map[string]string{
		"cache.proto": fmt.Sprintf(protoTemplate, "redis", "Redis"),
	})
}

// writeRedisV2Bundle creates the redis-pacto@2.0.0 bundle directory.
func writeRedisV2Bundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "redis-pacto-v2")
	return writeBundleDir(t, dir, redisContractV2, map[string]string{
		"cache.proto": fmt.Sprintf(protoTemplate, "redis", "Redis"),
	})
}

const openapiDiffContract = `pactoVersion: "1.0"
service:
  name: user-api
  version: "%s"
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`

// writeOpenAPIDiffBundleV1 creates a bundle with the v1 OpenAPI spec.
func writeOpenAPIDiffBundleV1(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "user-api-v1")
	return writeBundleDir(t, dir, fmt.Sprintf(openapiDiffContract, "1.0.0"), map[string]string{
		"openapi.yaml": openapiWithMethodsV1,
	})
}

// writeOpenAPIDiffBundleV2 creates a bundle with the v2 OpenAPI spec.
func writeOpenAPIDiffBundleV2(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "user-api-v2")
	return writeBundleDir(t, dir, fmt.Sprintf(openapiDiffContract, "2.0.0"), map[string]string{
		"openapi.yaml": openapiWithMethodsV2,
	})
}
