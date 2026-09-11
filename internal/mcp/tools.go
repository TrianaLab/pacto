package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v3/pkg/validation"
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
			"interfaces":                   {Type: "string", Description: "JSON array of interfaces: [{name, type, visibility?}]"},
			"dependencies":                 {Type: "string", Description: "JSON array of dependencies: [{name, ref, required?, compatibility?}] (name is derived from ref when omitted)"},
			"workload":                     {Type: "string", Description: "Workload type", Enum: []string{"service", "job", "scheduled"}},
			"stores_data":                  {Type: "boolean", Description: "Whether the service stores data (drives state model)"},
			"data_survives_restart":        {Type: "boolean", Description: "Whether data must survive pod restarts"},
			"data_shared_across_instances": {Type: "boolean", Description: "Whether data is shared across instances"},
			"data_loss_impact":             {Type: "string", Description: "Impact of data loss", Enum: []string{"low", "medium", "high"}},
			"config_properties":            {Type: "string", Description: "JSON array of config properties: [{name, type?, required?}]"},
			"metadata":                     {Type: "string", Description: "JSON object of metadata key-value pairs"},
			"dry_run":                      {Type: "boolean", Description: "If true, validate and return result without writing files"},
		}, []string{"name"}),
	}
}

// createArgs is the wire shape of pacto_create. The three intent booleans are
// pointers so an explicit false is distinguishable from an omitted argument:
// description inference only fills in what the caller left unsaid.
type createArgs struct {
	Name                      string `json:"name"`
	Description               string `json:"description"`
	Path                      string `json:"path"`
	Version                   string `json:"version"`
	Owner                     string `json:"owner"`
	Interfaces                string `json:"interfaces"`
	Dependencies              string `json:"dependencies"`
	Workload                  string `json:"workload"`
	StoresData                *bool  `json:"stores_data"`
	DataSurvivesRestart       *bool  `json:"data_survives_restart"`
	DataSharedAcrossInstances *bool  `json:"data_shared_across_instances"`
	DataLossImpact            string `json:"data_loss_impact"`
	ConfigProperties          string `json:"config_properties"`
	Metadata                  string `json:"metadata"`
	DryRun                    bool   `json:"dry_run"`
}

func createHandler() mcpsdk.ToolHandlerFor[createArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, args createArgs) (*mcpsdk.CallToolResult, any, error) {
		input := CreateInput{
			Name:                      args.Name,
			Description:               args.Description,
			Path:                      args.Path,
			Version:                   args.Version,
			Owner:                     args.Owner,
			Workload:                  args.Workload,
			StoresData:                args.StoresData,
			DataSurvivesRestart:       args.DataSurvivesRestart,
			DataSharedAcrossInstances: args.DataSharedAcrossInstances,
			DataLossImpact:            args.DataLossImpact,
			DryRun:                    args.DryRun,
		}

		if err := unmarshalFields([]jsonField{
			{"interfaces", args.Interfaces, &input.Interfaces},
			{"dependencies", args.Dependencies, &input.Dependencies},
			{"config_properties", args.ConfigProperties, &input.ConfigProperties},
			{"metadata", args.Metadata, &input.Metadata},
		}); err != nil {
			return nil, nil, err
		}

		result, err := Create(input)
		if err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(result)
		return res, nil, err
	}
}

// --- pacto_edit ---

func editTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_edit",
		Description: "Edits an existing Pacto contract. Supports adding/removing interfaces " +
			"and dependencies, changing workload and state semantics, updating metadata, and more. " +
			"Validates the result before writing. Supports dry_run mode.",
		InputSchema: inputSchema(map[string]property{
			"path":                         {Type: "string", Description: "Path to directory containing pacto.yaml (defaults to '.')"},
			"name":                         {Type: "string", Description: "New service name"},
			"version":                      {Type: "string", Description: "New service version"},
			"owner":                        {Type: "string", Description: "New owner identifier"},
			"add_interfaces":               {Type: "string", Description: "JSON array of interfaces to add: [{name, type, visibility?}]"},
			"remove_interfaces":            {Type: "string", Description: "JSON array of interface names to remove: [\"name1\", \"name2\"]"},
			"add_dependencies":             {Type: "string", Description: "JSON array of dependencies to add: [{name, ref, required?, compatibility?}] (name is derived from ref when omitted)"},
			"remove_dependencies":          {Type: "string", Description: "JSON array of dependency refs to remove: [\"ref1\", \"ref2\"]"},
			"workload":                     {Type: "string", Description: "New workload type", Enum: []string{"service", "job", "scheduled"}},
			"stores_data":                  {Type: "boolean", Description: "Whether the service stores data"},
			"data_survives_restart":        {Type: "boolean", Description: "Whether data must survive pod restarts"},
			"data_shared_across_instances": {Type: "boolean", Description: "Whether data is shared across instances"},
			"data_loss_impact":             {Type: "string", Description: "Impact of data loss", Enum: []string{"low", "medium", "high"}},
			"add_config_properties":        {Type: "string", Description: "JSON array of config properties to add: [{name, type?, required?}]"},
			"set_metadata":                 {Type: "string", Description: "JSON object of metadata to set"},
			"remove_metadata":              {Type: "string", Description: "JSON array of metadata keys to remove"},
			"dry_run":                      {Type: "boolean", Description: "If true, validate and return result without writing"},
		}, nil),
	}
}

// editArgs is the wire shape of pacto_edit. Every optional scalar is a pointer,
// so an argument the caller never sent leaves the contract's current value alone.
type editArgs struct {
	Path                      string  `json:"path"`
	Name                      *string `json:"name"`
	Version                   *string `json:"version"`
	Owner                     *string `json:"owner"`
	AddInterfaces             string  `json:"add_interfaces"`
	RemoveInterfaces          string  `json:"remove_interfaces"`
	AddDependencies           string  `json:"add_dependencies"`
	RemoveDependencies        string  `json:"remove_dependencies"`
	Workload                  *string `json:"workload"`
	StoresData                *bool   `json:"stores_data"`
	DataSurvivesRestart       *bool   `json:"data_survives_restart"`
	DataSharedAcrossInstances *bool   `json:"data_shared_across_instances"`
	DataLossImpact            *string `json:"data_loss_impact"`
	AddConfigProperties       string  `json:"add_config_properties"`
	SetMetadata               string  `json:"set_metadata"`
	RemoveMetadata            string  `json:"remove_metadata"`
	DryRun                    bool    `json:"dry_run"`
}

func editHandler() mcpsdk.ToolHandlerFor[editArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, args editArgs) (*mcpsdk.CallToolResult, any, error) {
		input := EditInput{
			Path:                      args.Path,
			Name:                      args.Name,
			Version:                   args.Version,
			Owner:                     args.Owner,
			Workload:                  args.Workload,
			StoresData:                args.StoresData,
			DataSurvivesRestart:       args.DataSurvivesRestart,
			DataSharedAcrossInstances: args.DataSharedAcrossInstances,
			DataLossImpact:            args.DataLossImpact,
			DryRun:                    args.DryRun,
		}

		if err := unmarshalFields([]jsonField{
			{"add_interfaces", args.AddInterfaces, &input.AddInterfaces},
			{"remove_interfaces", args.RemoveInterfaces, &input.RemoveInterfaces},
			{"add_dependencies", args.AddDependencies, &input.AddDependencies},
			{"remove_dependencies", args.RemoveDependencies, &input.RemoveDeps},
			{"add_config_properties", args.AddConfigProperties, &input.AddConfigProperties},
			{"set_metadata", args.SetMetadata, &input.SetMetadata},
			{"remove_metadata", args.RemoveMetadata, &input.RemoveMetadata},
		}); err != nil {
			return nil, nil, err
		}

		result, err := Edit(input)
		if err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(result)
		return res, nil, err
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

type checkArgs struct {
	Path string `json:"path"`
}

func checkHandler(resolverFor PolicyResolverFor) mcpsdk.ToolHandlerFor[checkArgs, any] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, args checkArgs) (*mcpsdk.CallToolResult, any, error) {
		result, err := Check(ctx, resolverFor, args.Path)
		if err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(result)
		return res, nil, err
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

const docsURL = "https://pacto.run"

type schemaResult struct {
	Description string `json:"description"`
	Docs        string `json:"docs"`
	JSONSchema  string `json:"jsonSchema"`
}

func schemaHandler() mcpsdk.ToolHandlerFor[struct{}, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
		result := schemaResult{
			Description: "Pacto is an operational contract format for cloud-native services. " +
				"A pacto.yaml file describes the service itself — interfaces, dependencies, " +
				"configuration, workload and state semantics, and policies. Use pacto_create to generate new contracts and pacto_edit to " +
				"modify existing ones. Use pacto_check to validate and get improvement suggestions.",
			Docs:       docsURL,
			JSONSchema: string(validation.SchemaBytes()),
		}
		res, err := jsonResult(result)
		return res, nil, err
	}
}

// --- schema helpers ---

type property struct {
	Type        string
	Description string
	Enum        []string // optional: closed set of allowed string values
}

// inputSchema builds the tool's declared JSON Schema. It is the schema the SDK
// validates every incoming call against, so a property's declared type and enum
// are enforced rather than advisory.
func inputSchema(props map[string]property, required []string) map[string]any {
	isRequired := make(map[string]bool, len(required))
	for _, name := range required {
		isRequired[name] = true
	}
	propMap := make(map[string]any, len(props))
	for name, p := range props {
		entry := map[string]any{
			"type":        any(p.Type),
			"description": p.Description,
		}
		// An LLM caller routinely sends an explicit null for an optional
		// argument it is not using. Rejecting that fails the whole call over the
		// one argument the caller said it did not want — the edit it did ask for
		// never happens — so for an optional argument null is declared valid and
		// decodes to the same zero value an omitted argument does. A required
		// argument keeps rejecting null: there "not provided" is the error.
		optional := !isRequired[name]
		if optional {
			entry["type"] = []string{p.Type, "null"}
		}
		if len(p.Enum) > 0 {
			vals := make([]any, 0, len(p.Enum)+1)
			for _, v := range p.Enum {
				vals = append(vals, v)
			}
			if optional {
				vals = append(vals, nil)
			}
			entry["enum"] = vals
		}
		propMap[name] = entry
	}
	schema := map[string]any{
		"type":       "object",
		"properties": propMap,
	}
	// An argument nobody declared is a mistake, most often a misremembered name.
	// Accepting it silently drops it and answers as if the caller had never asked
	// for it. A tool that declares no properties has no name to misremember, and
	// real clients send a dummy argument to zero-argument tools, so closing it
	// there rejects every call and buys nothing.
	if len(propMap) > 0 {
		schema["additionalProperties"] = false
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
