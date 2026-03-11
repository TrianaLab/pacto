package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// InitOptions holds options for the init command.
type InitOptions struct {
	Name string
}

// InitResult holds the result of the init command.
type InitResult struct {
	Dir  string
	Path string
}

const defaultContract = `pactoVersion: "1.0"

service:
  name: %s
  version: 0.1.0
  owner: team/my-team
  image:
    ref: ghcr.io/my-org/%s:0.1.0
    private: false

interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml

configuration:
  schema: configuration/schema.json

# dependencies:
#   - ref: oci://ghcr.io/my-org/other-service-pacto:1.0.0
#     required: true
#     compatibility: "^1.0.0"

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
  max: 3

metadata:
  team: my-team
  tier: standard
`

const defaultOpenAPI = `openapi: "3.0.0"
info:
  title: %s
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
`

const defaultConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
`

// Init scaffolds a new pacto project directory with the full bundle structure.
func (s *Service) Init(_ context.Context, opts InitOptions) (*InitResult, error) {
	name := opts.Name
	if name == "" {
		return nil, fmt.Errorf("service name is required")
	}
	slog.Debug("initializing new pacto project", "name", name)

	dir := name

	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("directory %q already exists", dir)
	}

	// Create the bundle directory structure.
	dirs := []string{
		dir,
		filepath.Join(dir, "interfaces"),
		filepath.Join(dir, "configuration"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	// Write pacto.yaml.
	pactoPath := filepath.Join(dir, "pacto.yaml")
	content := fmt.Sprintf(defaultContract, name, name)
	if err := writeFileFn(pactoPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write pacto.yaml: %w", err)
	}

	// Write placeholder contract and configuration files.
	openapiPath := filepath.Join(dir, "interfaces", "openapi.yaml")
	openapiContent := fmt.Sprintf(defaultOpenAPI, name)
	if err := writeFileFn(openapiPath, []byte(openapiContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", openapiPath, err)
	}

	schemaPath := filepath.Join(dir, "configuration", "schema.json")
	if err := writeFileFn(schemaPath, []byte(defaultConfigSchema), 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", schemaPath, err)
	}

	return &InitResult{Dir: dir, Path: pactoPath}, nil
}
