package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/pkg/validation"
)

// --- pacto_create ---

func createTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_create",
		Description: "Creates a new Pacto service contract from structured inputs. " +
			"Translates user intent (stores_data, data_survives_restart, etc.) into " +
			"correct contract primitives. Scaffolds pacto.yaml plus interface and " +
			"config files. Supports dry_run mode.",
		InputSchema: inputSchema(map[string]property{
			"name":                         {Type: "string", Description: "Service name (DNS-compatible, e.g. 'payment-api')"},
			"description":                  {Type: "string", Description: "Natural-language description for inference (e.g. 'REST API backed by postgres')"},
			"path":                         {Type: "string", Description: "Output directory (defaults to service name)"},
			"version":                      {Type: "string", Description: "Service version (defaults to '0.1.0')"},
			"owner":                        {Type: "string", Description: "Owner identifier (e.g. 'team/platform')"},
			"interfaces":                   {Type: "string", Description: "JSON array of interfaces: [{name, type, port?, visibility?}]"},
			"dependencies":                 {Type: "string", Description: "JSON array of dependencies: [{ref, required?, compatibility?}]"},
			"workload":                     {Type: "string", Description: "Workload type: service, job, or scheduled"},
			"stores_data":                  {Type: "boolean", Description: "Whether the service stores data (drives state model)"},
			"data_survives_restart":        {Type: "boolean", Description: "Whether data must survive pod restarts"},
			"data_shared_across_instances": {Type: "boolean", Description: "Whether data is shared across instances"},
			"data_loss_impact":             {Type: "string", Description: "Impact of data loss: low, medium, or high"},
			"config_properties":            {Type: "string", Description: "JSON array of config properties: [{name, type?, required?}]"},
			"replicas":                     {Type: "integer", Description: "Exact replica count"},
			"min_replicas":                 {Type: "integer", Description: "Minimum replicas for auto-scaling"},
			"max_replicas":                 {Type: "integer", Description: "Maximum replicas for auto-scaling"},
			"metadata":                     {Type: "string", Description: "JSON object of metadata key-value pairs"},
			"dry_run":                      {Type: "boolean", Description: "If true, validate and return result without writing files"},
		}, []string{"name"}),
	}
}

func createHandler() mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		input := CreateInput{
			Name:                      parseInput(req, "name"),
			Description:               parseInput(req, "description"),
			Path:                      parseInput(req, "path"),
			Version:                   parseInput(req, "version"),
			Owner:                     parseInput(req, "owner"),
			Workload:                  parseInput(req, "workload"),
			StoresData:                parseInputBool(req, "stores_data"),
			DataSurvivesRestart:       parseInputBool(req, "data_survives_restart"),
			DataSharedAcrossInstances: parseInputBool(req, "data_shared_across_instances"),
			DataLossImpact:            parseInput(req, "data_loss_impact"),
			DryRun:                    parseInputBool(req, "dry_run"),
		}

		if input.Name == "" {
			return errorResult(fmt.Errorf("name is required")), nil
		}

		// Parse JSON array fields
		if raw := parseInput(req, "interfaces"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &input.Interfaces); err != nil {
				return errorResult(fmt.Errorf("invalid interfaces JSON: %w", err)), nil
			}
		}
		if raw := parseInput(req, "dependencies"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &input.Dependencies); err != nil {
				return errorResult(fmt.Errorf("invalid dependencies JSON: %w", err)), nil
			}
		}
		if raw := parseInput(req, "config_properties"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &input.ConfigProperties); err != nil {
				return errorResult(fmt.Errorf("invalid config_properties JSON: %w", err)), nil
			}
		}
		if raw := parseInput(req, "metadata"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &input.Metadata); err != nil {
				return errorResult(fmt.Errorf("invalid metadata JSON: %w", err)), nil
			}
		}

		input.Replicas = parseInputIntPtr(req, "replicas")
		input.MinReplicas = parseInputIntPtr(req, "min_replicas")
		input.MaxReplicas = parseInputIntPtr(req, "max_replicas")

		result, err := Create(input)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_edit ---

func editTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_edit",
		Description: "Edits an existing Pacto contract. Supports adding/removing interfaces " +
			"and dependencies, changing runtime semantics, updating metadata, and more. " +
			"Validates the result before writing. Supports dry_run mode.",
		InputSchema: inputSchema(map[string]property{
			"path":                         {Type: "string", Description: "Path to directory containing pacto.yaml (defaults to '.')"},
			"name":                         {Type: "string", Description: "New service name"},
			"version":                      {Type: "string", Description: "New service version"},
			"owner":                        {Type: "string", Description: "New owner identifier"},
			"add_interfaces":               {Type: "string", Description: "JSON array of interfaces to add: [{name, type, port?, visibility?}]"},
			"remove_interfaces":            {Type: "string", Description: "JSON array of interface names to remove: [\"name1\", \"name2\"]"},
			"add_dependencies":             {Type: "string", Description: "JSON array of dependencies to add: [{ref, required?, compatibility?}]"},
			"remove_dependencies":          {Type: "string", Description: "JSON array of dependency refs to remove: [\"ref1\", \"ref2\"]"},
			"workload":                     {Type: "string", Description: "New workload type: service, job, or scheduled"},
			"stores_data":                  {Type: "boolean", Description: "Whether the service stores data"},
			"data_survives_restart":        {Type: "boolean", Description: "Whether data must survive pod restarts"},
			"data_shared_across_instances": {Type: "boolean", Description: "Whether data is shared across instances"},
			"data_loss_impact":             {Type: "string", Description: "Impact of data loss: low, medium, or high"},
			"add_config_properties":        {Type: "string", Description: "JSON array of config properties to add: [{name, type?, required?}]"},
			"replicas":                     {Type: "integer", Description: "Exact replica count"},
			"min_replicas":                 {Type: "integer", Description: "Minimum replicas for auto-scaling"},
			"max_replicas":                 {Type: "integer", Description: "Maximum replicas for auto-scaling"},
			"set_metadata":                 {Type: "string", Description: "JSON object of metadata to set"},
			"remove_metadata":              {Type: "string", Description: "JSON array of metadata keys to remove"},
			"dry_run":                      {Type: "boolean", Description: "If true, validate and return result without writing"},
		}, nil),
	}
}

// parseEditScalars extracts scalar (string/bool) fields into EditInput.
func parseEditScalars(req *mcpsdk.CallToolRequest, input *EditInput) {
	for _, f := range []struct {
		field string
		dst   **string
	}{
		{"name", &input.Name},
		{"version", &input.Version},
		{"owner", &input.Owner},
		{"workload", &input.Workload},
		{"data_loss_impact", &input.DataLossImpact},
	} {
		if s := parseInput(req, f.field); s != "" {
			*f.dst = &s
		}
	}
	for _, f := range []struct {
		field string
		dst   **bool
	}{
		{"stores_data", &input.StoresData},
		{"data_survives_restart", &input.DataSurvivesRestart},
		{"data_shared_across_instances", &input.DataSharedAcrossInstances},
	} {
		if parseInputHasField(req, f.field) {
			b := parseInputBool(req, f.field)
			*f.dst = &b
		}
	}
	input.Replicas = parseInputIntPtr(req, "replicas")
	input.MinReplicas = parseInputIntPtr(req, "min_replicas")
	input.MaxReplicas = parseInputIntPtr(req, "max_replicas")
}

// parseEditJSONFields extracts JSON array/object fields into EditInput.
func parseEditJSONFields(req *mcpsdk.CallToolRequest, input *EditInput) error {
	type jsonField struct {
		name string
		dst  interface{}
	}
	fields := []jsonField{
		{"add_interfaces", &input.AddInterfaces},
		{"remove_interfaces", &input.RemoveInterfaces},
		{"add_dependencies", &input.AddDependencies},
		{"remove_dependencies", &input.RemoveDeps},
		{"add_config_properties", &input.AddConfigProperties},
		{"set_metadata", &input.SetMetadata},
		{"remove_metadata", &input.RemoveMetadata},
	}
	for _, f := range fields {
		if raw := parseInput(req, f.name); raw != "" {
			if err := json.Unmarshal([]byte(raw), f.dst); err != nil {
				return fmt.Errorf("invalid %s JSON: %w", f.name, err)
			}
		}
	}
	return nil
}

func editHandler() mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		input := EditInput{
			Path:   parseInput(req, "path"),
			DryRun: parseInputBool(req, "dry_run"),
		}

		parseEditScalars(req, &input)

		if err := parseEditJSONFields(req, &input); err != nil {
			return errorResult(err), nil
		}

		result, err := Edit(input)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_check ---

func checkTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_check",
		Description: "Validates a Pacto contract and returns structured results including " +
			"errors, warnings, a contract summary, and actionable suggestions for " +
			"improvement with ready-to-use pacto_edit tool calls.",
		InputSchema: inputSchema(map[string]property{
			"path": {Type: "string", Description: "Path to directory containing pacto.yaml (defaults to '.')"},
		}, nil),
	}
}

func checkHandler() mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		path := parseInput(req, "path")
		result, err := Check(path)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_schema ---

func schemaTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_schema",
		Description: "Returns the Pacto contract JSON Schema and documentation link. " +
			"Call this FIRST before creating or editing contracts to understand the format.",
		InputSchema: inputSchema(map[string]property{}, nil),
	}
}

const docsURL = "https://trianalab.github.io/pacto"

type schemaResult struct {
	Description string `json:"description"`
	Docs        string `json:"docs"`
	JSONSchema  string `json:"jsonSchema"`
}

func schemaHandler() mcpsdk.ToolHandler {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		result := schemaResult{
			Description: "Pacto is an operational contract format for cloud-native services. " +
				"A pacto.yaml file describes the service itself — interfaces, dependencies, runtime semantics, " +
				"configuration, and scaling. Use pacto_create to generate new contracts and pacto_edit to " +
				"modify existing ones. Use pacto_check to validate and get improvement suggestions.",
			Docs:       docsURL,
			JSONSchema: string(validation.SchemaBytes()),
		}
		return jsonResult(result)
	}
}

// --- schema helpers ---

type property struct {
	Type        string
	Description string
}

func inputSchema(props map[string]property, required []string) map[string]any {
	propMap := make(map[string]any, len(props))
	for name, p := range props {
		propMap[name] = map[string]any{
			"type":        p.Type,
			"description": p.Description,
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": propMap,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// --- input parsing helpers ---

func parseInputIntPtr(req *mcpsdk.CallToolRequest, field string) *int {
	args := parseArgs(req)
	if args == nil {
		return nil
	}
	raw, ok := args[field]
	if !ok {
		return nil
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil
	}
	i := int(n)
	return &i
}

func parseInputHasField(req *mcpsdk.CallToolRequest, field string) bool {
	args := parseArgs(req)
	if args == nil {
		return false
	}
	_, ok := args[field]
	return ok
}
