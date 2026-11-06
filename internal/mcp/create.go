package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
	"gopkg.in/yaml.v3"
)

// fileOps is what the create, edit and check paths write files and encode YAML
// with: the os and yaml.v3 functions in production (osFileOps), failing fakes in
// tests, so no package state is swapped.
type fileOps struct {
	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
	rename    func(oldpath, newpath string) error
	dirFS     func(string) fs.FS
	marshal   func(any) ([]byte, error)
	unmarshal func([]byte, any) error
	mkdirTemp func(dir, pattern string) (string, error)
}

// osFileOps is the production fileOps.
func osFileOps() fileOps {
	return fileOps{os.MkdirAll, os.WriteFile, os.Rename, os.DirFS, yaml.Marshal, yaml.Unmarshal, os.MkdirTemp}
}

// --- Input/Output types ---

// CreateInput holds the parameters for creating a new contract.
type CreateInput struct {
	Path        string
	Name        string
	Description string
	Version     string
	Owner       string

	Interfaces   []InterfaceInput
	Dependencies []DependencyInput

	Workload string
	// The three intent booleans are only ever what the caller stated: the
	// description never infers state. A keyword such as "postgres" says the
	// service talks to a database, not that it keeps state itself, and a state
	// section the caller did not assert becomes a runtime contradiction the
	// moment persistence is observed. Unset reads as false (stateless).
	StoresData                *bool
	DataSurvivesRestart       *bool
	DataSharedAcrossInstances *bool
	DataLossImpact            string

	ConfigProperties []ConfigProperty
	Metadata         map[string]any

	DryRun bool
}

// InterfaceInput describes an interface to add.
type InterfaceInput struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Visibility string `json:"visibility,omitempty"`
}

// DependencyInput describes a dependency to add.
type DependencyInput struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
	// External marks a dependency this Pacto system does not deliver. With
	// no ref it needs Version, the free-text version it runs against.
	External bool   `json:"external,omitempty"`
	Version  string `json:"version,omitempty"`
}

// ConfigurationRefInput describes a configuration reference to add: a
// published contract whose configuration this service takes. Name defaults to
// the last segment of the ref's repository. Configuration selects which of the
// provider's configurations it takes and Values are the service's values for
// it; either raises pactoVersion to 2.6.
type ConfigurationRefInput struct {
	Name          string         `json:"name,omitempty"`
	Ref           string         `json:"ref"`
	Required      bool           `json:"required,omitempty"`
	Configuration string         `json:"configuration,omitempty"`
	Values        map[string]any `json:"values,omitempty"`
}

// EnvironmentInput describes an environment to add. File defaults to
// environments/<name>.yaml.
type EnvironmentInput struct {
	Name string `json:"name"`
	File string `json:"file,omitempty"`
}

// EnvironmentValuesInput is values to merge into one environment's file for
// one configuration. A null value removes its key.
type EnvironmentValuesInput struct {
	Environment   string         `json:"environment"`
	Configuration string         `json:"configuration"`
	Values        map[string]any `json:"values"`
}

// LifecycleInput sets service.lifecycle, or removes it when Clear is set.
type LifecycleInput struct {
	Deprecated string `json:"deprecated,omitempty"`
	Sunset     string `json:"sunset,omitempty"`
	Successor  string `json:"successor,omitempty"`
	Clear      bool   `json:"clear,omitempty"`
}

// PolicyRefInput describes a policy reference to add. Name defaults to the
// last segment of the ref's repository.
type PolicyRefInput struct {
	Name string `json:"name,omitempty"`
	Ref  string `json:"ref"`
}

// ConfigProperty describes a configuration property.
type ConfigProperty struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// CreateResult holds the result of a create operation.
type CreateResult struct {
	Path      string          `json:"path"`
	Summary   ContractSummary `json:"summary"`
	Derived   []string        `json:"derived"`
	FileCount int             `json:"fileCount"`
	// Warnings are what the contract says that is valid but likely wrong.
	Warnings []string `json:"warnings,omitempty"`
}

// EditInput holds the parameters for editing an existing contract.
type EditInput struct {
	Path string

	Name    *string
	Version *string
	Owner   *string
	// Domain sets service.domain and raises an older pactoVersion to 2.4.
	Domain *string
	// Offering and Summary set service.offering and service.summary and
	// raise an older pactoVersion to 2.5.
	Offering *string
	Summary  *string

	AddInterfaces           []InterfaceInput
	RemoveInterfaces        []string
	AddDependencies         []DependencyInput
	RemoveDeps              []string
	AddConfigurationRefs    []ConfigurationRefInput
	RemoveConfigurationRefs []string
	AddPolicyRefs           []PolicyRefInput
	AddEnvironments         []EnvironmentInput
	SetEnvironmentValues    []EnvironmentValuesInput
	// Lifecycle sets or clears service.lifecycle and raises an older
	// pactoVersion to 2.6.
	Lifecycle *LifecycleInput

	Workload                  *string
	StoresData                *bool
	DataSurvivesRestart       *bool
	DataSharedAcrossInstances *bool
	DataLossImpact            *string

	AddConfigProperties []ConfigProperty
	SetMetadata         map[string]any
	RemoveMetadata      []string

	DryRun bool
}

// EditResult holds the result of an edit operation.
type EditResult struct {
	Path    string          `json:"path"`
	Summary ContractSummary `json:"summary"`
	Changes []string        `json:"changes"`
	// Warnings are what the edit wrote that is valid but likely wrong.
	Warnings []string `json:"warnings,omitempty"`
}

// CheckResult holds the result of a check operation.
type CheckResult struct {
	Valid       bool             `json:"valid"`
	Summary     ContractSummary  `json:"summary"`
	Errors      []ValidationItem `json:"errors,omitempty"`
	Warnings    []ValidationItem `json:"warnings,omitempty"`
	Suggestions []Suggestion     `json:"suggestions,omitempty"`
	// Stages and Next are what `pacto validate` reports: each stage's status
	// and the one command to run now.
	Stages []validation.Stage `json:"stages,omitempty"`
	Next   string             `json:"next,omitempty"`
}

// ContractSummary provides a high-level overview of a contract.
type ContractSummary struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Owner        string            `json:"owner,omitempty"`
	Domain       string            `json:"domain,omitempty"`
	Offering     string            `json:"offering,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Interfaces   []string          `json:"interfaces,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty"`
	Environments []string          `json:"environments,omitempty"`
	Workload     string            `json:"workload,omitempty"`
	StateType    string            `json:"stateType,omitempty"`
	Sections     map[string]string `json:"sections"`
}

// ValidationItem represents a single validation error or warning.
type ValidationItem struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	// File is the bundle file Line and Column point into when it is not
	// pacto.yaml, such as an environment's file.
	File string `json:"file,omitempty"`
}

// Suggestion represents an improvement suggestion with an optional tool call.
type Suggestion struct {
	Message  string    `json:"message"`
	ToolCall *ToolCall `json:"toolCall,omitempty"`
}

// ToolCall represents a suggested MCP tool invocation.
type ToolCall struct {
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

// --- Description inference ---

type descriptionHints struct {
	hasHTTP     bool
	hasGRPC     bool
	hasEvents   bool
	isWorker    bool
	isScheduled bool
}

// descriptionPatterns are the keyword heuristics pacto_create applies to a
// description; docs/mcp-integration.md lists them for agents. They only infer
// interfaces and the workload, never state: storage keywords used to set
// stores_data and friends, asserting a state model nobody declared.
var descriptionPatterns = []struct {
	pattern *regexp.Regexp
	apply   func(*descriptionHints)
}{
	{regexp.MustCompile(`(?i)\b(REST|HTTP|API|web)\b`), func(h *descriptionHints) { h.hasHTTP = true }},
	{regexp.MustCompile(`(?i)\bgRPC\b`), func(h *descriptionHints) { h.hasGRPC = true }},
	{regexp.MustCompile(`(?i)\b(event|kafka|nats|pubsub|queue|message|rabbit)\b`), func(h *descriptionHints) { h.hasEvents = true }},
	{regexp.MustCompile(`(?i)\b(worker|consumer|processor)\b`), func(h *descriptionHints) { h.isWorker = true }},
	{regexp.MustCompile(`(?i)\b(cron|scheduled|periodic|batch)\b`), func(h *descriptionHints) { h.isScheduled = true }},
}

func inferFromDescription(desc string) descriptionHints {
	var h descriptionHints
	for _, p := range descriptionPatterns {
		if p.pattern.MatchString(desc) {
			p.apply(&h)
		}
	}
	return h
}

// --- State derivation ---

type stateIntent struct {
	storesData                bool
	dataSurvivesRestart       bool
	dataSharedAcrossInstances bool
	dataLossImpact            string
}

func deriveStateMap(intent stateIntent) map[string]any {
	stateType := contract.StateStateless
	scope := contract.ScopeLocal
	durability := contract.DurabilityEphemeral
	dataCrit := contract.DataCriticalityLow

	if intent.storesData {
		stateType = contract.StateStateful
		if intent.dataSurvivesRestart {
			durability = contract.DurabilityPersistent
		}
		if intent.dataSharedAcrossInstances {
			scope = contract.ScopeShared
		}
		dataCrit = contract.DataCriticalityMedium
	}

	if intent.dataLossImpact != "" {
		dataCrit = intent.dataLossImpact
	}

	return map[string]any{
		"type": stateType,
		"persistence": map[string]any{
			"scope":      scope,
			"durability": durability,
		},
		"dataCriticality": dataCrit,
	}
}

// --- Create ---

// Create builds a new pacto contract from structured input.
func Create(input CreateInput) (*CreateResult, error) { return osFileOps().create(input) }

func (o fileOps) create(input CreateInput) (*CreateResult, error) {
	if input.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	dir := input.Path
	if dir == "" {
		dir = input.Name
	}

	// Apply description inference (explicit inputs override)
	hints := inferFromDescription(input.Description)
	applyHintsToCreate(&input, hints)

	// Build the contract map
	m := buildCreateMap(input)

	// Marshal to YAML
	yamlBytes, err := o.marshalContract(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contract: %w", err)
	}

	// Validate before writing
	c, err := validateYAML(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("generated contract is invalid: %w", err)
	}

	derived := collectDerived(input, hints)

	if input.DryRun {
		return &CreateResult{
			Path:      filepath.Join(dir, "pacto.yaml"),
			Summary:   summarizeContract(c),
			Derived:   derived,
			FileCount: 0,
			Warnings:  ownerWarnings(input.Owner),
		}, nil
	}

	// Write files
	fileCount, err := o.writeBundle(dir, yamlBytes, input)
	if err != nil {
		return nil, err
	}

	return &CreateResult{
		Path:      filepath.Join(dir, "pacto.yaml"),
		Summary:   summarizeContract(c),
		Derived:   derived,
		FileCount: fileCount,
		Warnings:  ownerWarnings(input.Owner),
	}, nil
}

func applyHintsToCreate(input *CreateInput, h descriptionHints) {
	if len(input.Interfaces) == 0 {
		if h.hasHTTP {
			input.Interfaces = append(input.Interfaces, InterfaceInput{
				Name: "http-api", Type: contract.InterfaceTypeOpenAPI, Visibility: contract.VisibilityPublic,
			})
		}
		if h.hasGRPC {
			input.Interfaces = append(input.Interfaces, InterfaceInput{
				Name: "grpc-api", Type: contract.InterfaceTypeGRPC, Visibility: contract.VisibilityInternal,
			})
		}
		if h.hasEvents {
			input.Interfaces = append(input.Interfaces, InterfaceInput{
				Name: "events", Type: contract.InterfaceTypeAsyncAPI,
			})
		}
	}

	if input.Workload == "" {
		if h.isScheduled {
			input.Workload = contract.WorkloadScheduled
		} else if h.isWorker {
			input.Workload = contract.WorkloadJob
		}
	}
}

// boolOrFalse reads an optional intent flag, treating "unsaid" as false.
func boolOrFalse(p *bool) bool {
	return p != nil && *p
}

func buildCreateMap(input CreateInput) map[string]any {
	m := map[string]any{
		"pactoVersion": "2.0",
		"service": map[string]any{
			"name":    input.Name,
			"version": defaultVersion(input.Version),
		},
	}

	svc := m["service"].(map[string]any)
	if input.Owner != "" {
		svc["owner"] = map[string]any{"team": input.Owner}
	}

	if len(input.Interfaces) > 0 {
		m["interfaces"] = buildInterfacesList(input.Interfaces)
	}

	if len(input.Dependencies) > 0 {
		m["dependencies"] = buildDependenciesList(input.Dependencies)
		needs21(m, input.Dependencies)
	}

	workload := input.Workload
	if workload == "" {
		workload = contract.WorkloadService
	}
	m["workload"] = workload

	intent := stateIntent{
		storesData:                boolOrFalse(input.StoresData),
		dataSurvivesRestart:       boolOrFalse(input.DataSurvivesRestart),
		dataSharedAcrossInstances: boolOrFalse(input.DataSharedAcrossInstances),
		dataLossImpact:            input.DataLossImpact,
	}
	m["state"] = deriveStateMap(intent)

	if len(input.ConfigProperties) > 0 {
		m["configurations"] = []any{
			map[string]any{
				"name":     "default",
				"schema":   "configuration/schema.json",
				"required": true,
			},
		}
	}

	if len(input.Metadata) > 0 {
		m["metadata"] = input.Metadata
	}

	return m
}

func buildInterfacesList(inputs []InterfaceInput) []any {
	var result []any
	for _, iface := range inputs {
		entry := map[string]any{
			"name": iface.Name,
			"type": iface.Type,
			"ref":  interfaceRefPath(iface),
		}
		if iface.Visibility != "" {
			entry["visibility"] = iface.Visibility
		}
		result = append(result, entry)
	}
	return result
}

func interfaceRefPath(iface InterfaceInput) string {
	return fmt.Sprintf("interfaces/%s.yaml", iface.Name)
}

// dependencyName returns the dependency's explicit name, or derives one from
// the ref (last path segment, without the oci:// scheme, tag, or digest) when
// omitted — so an agent that supplies only a ref still produces a contract that
// passes structural validation (which requires dependencies[].name).
func dependencyName(dep DependencyInput) string {
	if dep.Name != "" {
		return dep.Name
	}
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(dep.Ref))
	return repo[strings.LastIndex(repo, "/")+1:]
}

func buildDependenciesList(inputs []DependencyInput) []any {
	var result []any
	for _, dep := range inputs {
		result = append(result, dependencyEntry(dep))
	}
	return result
}

// dependencyEntry is one dependencies[] entry. An external dependency with no
// ref carries its version instead of a ref and a range.
func dependencyEntry(dep DependencyInput) map[string]any {
	if dep.Ref == "" && dep.External {
		return map[string]any{"name": dep.Name, "external": true, "version": dep.Version, "required": dep.Required}
	}
	entry := map[string]any{
		"name":          dependencyName(dep),
		"ref":           dep.Ref,
		"compatibility": defaultCompatibility(dep.Compatibility),
		"required":      dep.Required, // mandatory in v2 schema; emit true AND false
	}
	if dep.External {
		entry["external"] = true
	}
	return entry
}

// needs21 sets pactoVersion 2.1 when an external dependency is declared:
// 2.0 has no external dependencies, and 2.1 keeps every 2.0 field.
func needs21(m map[string]any, deps []DependencyInput) bool {
	if m["pactoVersion"] != "2.0" || !slices.ContainsFunc(deps, func(d DependencyInput) bool { return d.External }) {
		return false
	}
	m["pactoVersion"] = "2.1"
	return true
}

// --- Edit ---

// validateEdited validates an edited contract over its bundle: the existing
// files, the new YAML, the files the edit writes and stubs for new refs. When
// resolve is set (see needsResolve) the edited bundle must also pass validate,
// the check pacto_check runs, so the values are checked against the resolved
// provider schema. A nil validate resolves configuration refs with no
// resolver, so a ref the contract depends on fails closed with
// CONFIG_REF_UNRESOLVED.
func (o fileOps) validateEdited(ctx context.Context, validate Validator, dir string, yamlBytes []byte, c *contract.Contract, files map[string][]byte, resolve bool) error {
	bundleFS := o.buildBundleFSForValidation(dir, yamlBytes, c, files)
	result := validation.Validate(c, yamlBytes, bundleFS)
	if result.IsValid() && resolve {
		if validate == nil {
			_, result = validation.ResolveConfigRefs(ctx, c, bundleFS, validation.ConfigRefOptions{Strict: true})
		} else {
			report, err := o.validateStaged(ctx, validate, dir, bundleFS)
			if err != nil {
				return err
			}
			result = report.ValidationResult
		}
	}
	if !result.IsValid() {
		return fmt.Errorf("edited contract fails validation: %s: %s", result.Errors[0].Code, result.Errors[0].Message)
	}
	return nil
}

// validateStaged writes bundleFS to a temporary directory beside dir, so a
// relative ref resolves from it as it does from dir, runs validate on it and
// removes it.
func (o fileOps) validateStaged(ctx context.Context, validate Validator, dir string, bundleFS fstest.MapFS) (validation.Report, error) {
	abs, _ := filepath.Abs(dir) //nolint:errcheck // fails only when the working directory is gone, and then the stage fails below
	stage, err := o.mkdirTemp(filepath.Dir(abs), ".pacto-edit-*")
	if err != nil {
		return validation.Report{}, fmt.Errorf("failed to stage the edited bundle: %w", err)
	}
	defer os.RemoveAll(stage) //nolint:errcheck // best-effort cleanup of a temporary directory
	for _, rel := range slices.Sorted(maps.Keys(bundleFS)) {
		p := filepath.Join(stage, filepath.FromSlash(rel))
		if bundleFS[rel].Mode.IsDir() {
			err = o.mkdirAll(p, 0o755)
		} else if err = o.mkdirAll(filepath.Dir(p), 0o755); err == nil {
			err = o.writeFile(p, bundleFS[rel].Data, 0o644)
		}
		if err != nil {
			return validation.Report{}, fmt.Errorf("failed to stage the edited bundle: %w", err)
		}
	}
	return validate(ctx, stage)
}

// needsResolve reports whether input adds or changes something configuration
// resolution depends on: a configuration ref, an environment (its file may
// already hold values) or environment values. Only a resolved schema can
// check those, so removals and lifecycle edits keep the local check.
func needsResolve(input EditInput) bool {
	return len(input.AddConfigurationRefs) > 0 || len(input.AddEnvironments) > 0 || len(input.SetEnvironmentValues) > 0
}

// Edit modifies an existing pacto contract. validate is the check pacto_check
// runs (see validateEdited); nothing is written unless the edited bundle passes.
func Edit(ctx context.Context, validate Validator, input EditInput) (*EditResult, error) {
	return osFileOps().edit(ctx, validate, input)
}

func (o fileOps) edit(ctx context.Context, validate Validator, input EditInput) (*EditResult, error) {
	dir := input.Path
	if dir == "" {
		dir = "."
	}

	pactoPath := filepath.Join(dir, "pacto.yaml")
	rawYAML, err := os.ReadFile(pactoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pactoPath, err)
	}

	// contract.DecodeYAML, not yaml.Unmarshal: an unquoted date scalar
	// (readiness.expires, readiness.history[].date — what `pacto init` scaffolds)
	// would otherwise decode to time.Time and be written back as RFC3339, which
	// readiness validation then rejects.
	var m map[string]any
	if err := contract.DecodeYAML(rawYAML, &m); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", pactoPath, err)
	}

	files, changes, err := editFiles(dir, m, input)
	if err != nil {
		return nil, err
	}

	yamlBytes, err := o.marshalContract(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contract: %w", err)
	}

	// Validate through Parse first
	c, err := contract.Parse(bytes.NewReader(yamlBytes))
	if err != nil {
		return nil, fmt.Errorf("edited contract is invalid: %w", err)
	}

	if err := o.validateEdited(ctx, validate, dir, yamlBytes, c, files, needsResolve(input)); err != nil {
		return nil, err
	}

	result := &EditResult{Path: pactoPath, Summary: summarizeContract(c), Changes: changes}
	if input.Owner != nil {
		result.Warnings = ownerWarnings(*input.Owner)
	}
	if input.DryRun {
		return result, nil
	}

	files["pacto.yaml"] = yamlBytes
	if err := o.writeFiles(dir, files); err != nil {
		return nil, err
	}

	// Scaffold any new interface contract files
	if err := o.scaffoldNewInterfaceFiles(dir, input.AddInterfaces); err != nil {
		return nil, err
	}
	return result, nil
}

// editFiles applies input's edits to m and returns the bundle files the edit
// writes beside pacto.yaml (a merged config schema and every new or edited
// environment file) with the changes made.
func editFiles(dir string, m map[string]any, input EditInput) (map[string][]byte, []string, error) {
	before := configNames(m)
	changes, err := applyEdits(m, input)
	if err != nil {
		return nil, nil, err
	}
	files := map[string][]byte{}
	if len(input.AddConfigProperties) > 0 {
		rel, data, err := mergeConfigSchema(dir, m, input.AddConfigProperties)
		if err != nil {
			return nil, nil, err
		}
		files[rel] = data
	}
	gone := slices.DeleteFunc(before, func(name string) bool { return slices.Contains(configNames(m), name) })
	envChanges, err := environmentFiles(dir, m, input, gone, files)
	if err != nil {
		return nil, nil, err
	}
	return files, append(changes, envChanges...), nil
}

func applyEdits(m map[string]any, input EditInput) ([]string, error) {
	var changes []string

	changes = append(changes, applyServiceEdits(m, input)...)

	// Interfaces
	if len(input.RemoveInterfaces) > 0 {
		changes = append(changes, removeInterfaces(m, input.RemoveInterfaces)...)
	}
	if len(input.AddInterfaces) > 0 {
		changes = append(changes, addInterfaces(m, input.AddInterfaces)...)
	}

	// Dependencies
	if len(input.RemoveDeps) > 0 {
		changes = append(changes, removeDependencies(m, input.RemoveDeps)...)
	}
	if len(input.AddDependencies) > 0 {
		changes = append(changes, addDependencies(m, input.AddDependencies)...)
	}
	refChanges, err := applyReferenceEdits(m, input)
	if err != nil {
		return nil, err
	}
	changes = append(changes, refChanges...)

	// Workload and state
	if input.Workload != nil {
		m["workload"] = *input.Workload
		changes = append(changes, fmt.Sprintf("set workload to %q", *input.Workload))
	}
	if hasStateEdits(input) {
		changes = append(changes, applyStateEdits(m, input)...)
	}

	// Config properties
	if len(input.AddConfigProperties) > 0 {
		ensureConfigSection(m)
		changes = append(changes, "added configuration properties")
	}

	// Metadata
	if len(input.SetMetadata) > 0 || len(input.RemoveMetadata) > 0 {
		changes = append(changes, applyMetadataEdits(m, input.SetMetadata, input.RemoveMetadata)...)
	}

	return changes, nil
}

// applyReferenceEdits applies the configuration-reference, policy-reference
// and environment edits.
func applyReferenceEdits(m map[string]any, input EditInput) ([]string, error) {
	changes, err := removeConfigurationRefs(m, input.RemoveConfigurationRefs)
	if err != nil {
		return nil, err
	}
	changes = append(changes, addConfigurationRefs(m, input.AddConfigurationRefs)...)
	changes = append(changes, addPolicyRefs(m, input.AddPolicyRefs)...)
	envChanges, err := addEnvironments(m, input.AddEnvironments)
	return append(changes, envChanges...), err
}

// applyServiceEdits applies the edits to the service block.
func applyServiceEdits(m map[string]any, input EditInput) []string {
	var changes []string
	svc, _ := m["service"].(map[string]any)
	if svc == nil {
		svc = map[string]any{}
		m["service"] = svc
	}
	if input.Name != nil {
		svc["name"] = *input.Name
		changes = append(changes, fmt.Sprintf("renamed service to %q", *input.Name))
	}
	if input.Version != nil {
		svc["version"] = *input.Version
		changes = append(changes, fmt.Sprintf("set version to %s", *input.Version))
	}
	if input.Owner != nil {
		svc["owner"] = map[string]any{"team": *input.Owner}
		changes = append(changes, fmt.Sprintf("set owner to %q", *input.Owner))
	}
	if input.Domain != nil {
		changes = append(changes, setServiceField(m, svc, "domain", *input.Domain, "2.4")...)
	}
	if input.Offering != nil {
		changes = append(changes, setServiceField(m, svc, "offering", *input.Offering, "2.5")...)
	}
	if input.Summary != nil {
		changes = append(changes, setServiceField(m, svc, "summary", *input.Summary, "2.5")...)
	}
	if input.Lifecycle != nil {
		changes = append(changes, setLifecycle(m, svc, *input.Lifecycle)...)
	}
	return changes
}

// setLifecycle sets service.lifecycle, raising an older pactoVersion to 2.6,
// or removes it when in.Clear is set.
func setLifecycle(m, svc map[string]any, in LifecycleInput) []string {
	if in.Clear {
		delete(svc, "lifecycle")
		return []string{"removed service lifecycle"}
	}
	lc := contract.InterfaceLifecycle{Deprecated: in.Deprecated, Sunset: in.Sunset, Successor: in.Successor}
	svc["lifecycle"] = lc
	return append(raiseVersion(m, "2.6"), "set service lifecycle to "+lc.String())
}

func removeInterfaces(m map[string]any, names []string) []string {
	var changes []string
	ifaces, ok := m["interfaces"].([]any)
	if !ok {
		return nil
	}
	removeSet := make(map[string]bool, len(names))
	for _, n := range names {
		removeSet[n] = true
	}
	var kept []any
	for _, iface := range ifaces {
		if ifaceMap, ok := iface.(map[string]any); ok {
			if name, ok := ifaceMap["name"].(string); ok && removeSet[name] {
				changes = append(changes, fmt.Sprintf("removed interface %q", name))
				continue
			}
		}
		kept = append(kept, iface)
	}
	if len(kept) == 0 {
		delete(m, "interfaces")
	} else {
		m["interfaces"] = kept
	}
	return changes
}

func addInterfaces(m map[string]any, inputs []InterfaceInput) []string {
	var changes []string
	ifaces, _ := m["interfaces"].([]any)
	for _, iface := range inputs {
		entry := map[string]any{
			"name": iface.Name,
			"type": iface.Type,
			"ref":  interfaceRefPath(iface),
		}
		if iface.Visibility != "" {
			entry["visibility"] = iface.Visibility
		}
		ifaces = append(ifaces, entry)
		changes = append(changes, fmt.Sprintf("added interface %q (%s)", iface.Name, iface.Type))
	}
	m["interfaces"] = ifaces
	return changes
}

func removeDependencies(m map[string]any, refs []string) []string {
	var changes []string
	deps, ok := m["dependencies"].([]any)
	if !ok {
		return nil
	}
	removeSet := make(map[string]bool, len(refs))
	for _, r := range refs {
		removeSet[r] = true
	}
	var kept []any
	for _, dep := range deps {
		if depMap, ok := dep.(map[string]any); ok {
			if ref, ok := depMap["ref"].(string); ok && removeSet[ref] {
				changes = append(changes, fmt.Sprintf("removed dependency %q", ref))
				continue
			}
			// An external dependency with no ref is removed by name.
			if name, ok := depMap["name"].(string); ok && depMap["ref"] == nil && removeSet[name] {
				changes = append(changes, fmt.Sprintf("removed dependency %q", name))
				continue
			}
		}
		kept = append(kept, dep)
	}
	if len(kept) == 0 {
		delete(m, "dependencies")
	} else {
		m["dependencies"] = kept
	}
	return changes
}

func addDependencies(m map[string]any, inputs []DependencyInput) []string {
	var changes []string
	deps, _ := m["dependencies"].([]any)
	for _, dep := range inputs {
		entry := dependencyEntry(dep)
		deps = append(deps, entry)
		changes = append(changes, fmt.Sprintf("added dependency %q", entry["name"]))
	}
	m["dependencies"] = deps
	if needs21(m, inputs) {
		changes = append(changes, `set pactoVersion "2.1" for the external dependency`)
	}
	return changes
}

// setServiceField sets service.<field> and, in a contract older than
// pactoVersion minVersion, raises pactoVersion to minVersion, which keeps
// every older field.
func setServiceField(m, svc map[string]any, field, value, minVersion string) []string {
	changes := raiseVersion(m, minVersion)
	svc[field] = value
	return append(changes, fmt.Sprintf("set %s to %q", field, value))
}

// raiseVersion raises a pactoVersion older than minVersion to minVersion,
// which keeps every older field, and returns the change it made.
func raiseVersion(m map[string]any, minVersion string) []string {
	if v, _ := m["pactoVersion"].(string); slices.Index(contract.SupportedVersions, v) < slices.Index(contract.SupportedVersions, minVersion) {
		m["pactoVersion"] = minVersion
		return []string{fmt.Sprintf("set pactoVersion to %q", minVersion)}
	}
	return nil
}

// addConfigurationRefs appends a configurations[] entry for each reference. A
// provider configuration selector or values raise pactoVersion to 2.6.
func addConfigurationRefs(m map[string]any, inputs []ConfigurationRefInput) []string {
	if len(inputs) == 0 {
		return nil
	}
	var changes []string
	configs, _ := m["configurations"].([]any)
	for _, in := range inputs {
		name := dependencyName(DependencyInput{Name: in.Name, Ref: in.Ref})
		entry := map[string]any{"name": name, "ref": in.Ref, "required": in.Required}
		if in.Configuration != "" || len(in.Values) > 0 {
			changes = append(changes, raiseVersion(m, "2.6")...)
		}
		if in.Configuration != "" {
			entry["configuration"] = in.Configuration
		}
		if len(in.Values) > 0 {
			entry["values"] = in.Values
		}
		configs = append(configs, entry)
		changes = append(changes, fmt.Sprintf("added configuration reference %q", name))
	}
	m["configurations"] = configs
	return changes
}

// removeConfigurationRefs removes each configuration reference named or
// pointing at one of keys.
func removeConfigurationRefs(m map[string]any, keys []string) ([]string, error) {
	var changes []string
	for _, key := range keys {
		configs, _ := m["configurations"].([]any)
		var kept []any
		for _, cfg := range configs {
			cm, _ := cfg.(map[string]any)
			if ref, _ := cm["ref"].(string); ref != "" && (cm["name"] == key || ref == key) {
				changes = append(changes, fmt.Sprintf("removed configuration reference %q", cm["name"]))
				continue
			}
			kept = append(kept, cfg)
		}
		if len(kept) == len(configs) {
			return nil, fmt.Errorf("no configuration reference named or pointing at %q", key)
		}
		if len(kept) == 0 {
			delete(m, "configurations")
		} else {
			m["configurations"] = kept
		}
	}
	return changes, nil
}

// configNames is the name of each configuration m declares.
func configNames(m map[string]any) []string {
	var names []string
	configs, _ := m["configurations"].([]any)
	for _, cfg := range configs {
		cm, _ := cfg.(map[string]any)
		if name, _ := cm["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// declaredEnvironments is each environment m declares.
func declaredEnvironments(m map[string]any) []contract.Environment {
	var envs []contract.Environment
	list, _ := m["environments"].([]any)
	for _, e := range list {
		em, _ := e.(map[string]any)
		name, _ := em["name"].(string)
		file, _ := em["file"].(string)
		envs = append(envs, contract.Environment{Name: name, File: file})
	}
	return envs
}

// addEnvironments declares each environment, its file defaulting to
// environments/<name>.yaml, and raises an older pactoVersion to 2.3.
func addEnvironments(m map[string]any, inputs []EnvironmentInput) ([]string, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	list, _ := m["environments"].([]any)
	changes := raiseVersion(m, "2.3")
	for _, in := range inputs {
		if slices.ContainsFunc(declaredEnvironments(m), func(e contract.Environment) bool { return e.Name == in.Name }) {
			return nil, fmt.Errorf("environment %q is already declared", in.Name)
		}
		file := cmp.Or(in.File, "environments/"+in.Name+".yaml")
		list = append(list, map[string]any{"name": in.Name, "file": file})
		m["environments"] = list
		changes = append(changes, fmt.Sprintf("added environment %q (%s)", in.Name, file))
	}
	return changes, nil
}

// environmentFiles adds to files the file of each environment the edit adds
// (an empty object when it is not on disk yet) and each environment file whose
// values the edit changes: values set through set_environment_values, merged
// as an environment merges over defaults (a null removes its key), and the
// values of every configuration in gone, which the edit removed. It reads the
// edited contract, so values may go to an environment or configuration the
// same edit adds.
func environmentFiles(dir string, m map[string]any, input EditInput, gone []string, files map[string][]byte) ([]string, error) {
	envs := declaredEnvironments(m)
	for _, in := range input.SetEnvironmentValues {
		if !slices.ContainsFunc(envs, func(e contract.Environment) bool { return e.Name == in.Environment }) {
			return nil, fmt.Errorf("environment %q is not declared; add it with add_environments", in.Environment)
		}
		if !slices.Contains(configNames(m), in.Configuration) {
			return nil, fmt.Errorf("configuration %q is not declared", in.Configuration)
		}
	}
	var changes []string
	for _, env := range envs {
		values, exists, err := readEnvironmentValues(dir, env)
		if err != nil {
			return nil, err
		}
		edited := editEnvironmentValues(env.Name, values, input.SetEnvironmentValues, gone)
		added := slices.ContainsFunc(input.AddEnvironments, func(a EnvironmentInput) bool { return a.Name == env.Name })
		if len(edited) > 0 || (added && !exists) {
			files[env.File], _ = yaml.Marshal(values) //nolint:errcheck // values decoded from YAML or JSON always encode
			changes = append(changes, edited...)
		}
	}
	return changes, nil
}

// readEnvironmentValues reads env's file under dir. A file that is not there
// yet holds no values.
func readEnvironmentValues(dir string, env contract.Environment) (map[string]any, bool, error) {
	if !filepath.IsLocal(env.File) {
		return nil, false, fmt.Errorf("environment %q: file %q is outside the bundle", env.Name, env.File)
	}
	raw, err := os.ReadFile(filepath.Join(dir, env.File))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to read %s: %w", env.File, err)
	}
	values, err := contract.ParseEnvironmentValues(raw)
	if err != nil {
		return nil, false, fmt.Errorf("environment %q: cannot read %s: %w", env.Name, env.File, err)
	}
	return values, true, nil
}

// editEnvironmentValues removes the values of every configuration in gone
// from one environment's values and merges in the values sets give it, and
// returns the changes.
func editEnvironmentValues(env string, values map[string]any, sets []EnvironmentValuesInput, gone []string) []string {
	var changes []string
	for _, name := range gone {
		if _, ok := values[name]; ok {
			delete(values, name)
			changes = append(changes, fmt.Sprintf("removed values for configuration %q from environment %q", name, env))
		}
	}
	for _, in := range sets {
		if in.Environment != env {
			continue
		}
		cfg, _ := values[in.Configuration].(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
		}
		validation.MergeValues(cfg, in.Values)
		values[in.Configuration] = cfg
		if len(cfg) == 0 {
			delete(values, in.Configuration)
		}
		changes = append(changes, fmt.Sprintf("set values for configuration %q in environment %q", in.Configuration, env))
	}
	return changes
}

// addPolicyRefs appends a policies[] entry for each reference.
func addPolicyRefs(m map[string]any, inputs []PolicyRefInput) []string {
	if len(inputs) == 0 {
		return nil
	}
	var changes []string
	policies, _ := m["policies"].([]any)
	for _, in := range inputs {
		name := dependencyName(DependencyInput{Name: in.Name, Ref: in.Ref})
		policies = append(policies, map[string]any{"name": name, "ref": in.Ref})
		changes = append(changes, fmt.Sprintf("added policy reference %q", name))
	}
	m["policies"] = policies
	return changes
}

func applyStateEdits(m map[string]any, input EditInput) []string {
	intent := buildStateIntentFromCurrent(m, input)
	m["state"] = deriveStateMap(intent)
	return []string{"updated state model"}
}

func hasStateEdits(input EditInput) bool {
	return input.StoresData != nil || input.DataSurvivesRestart != nil ||
		input.DataSharedAcrossInstances != nil || input.DataLossImpact != nil
}

func buildStateIntentFromCurrent(m map[string]any, input EditInput) stateIntent {
	storesData, dataSurvives, dataShared, dataLossImpact := readCurrentState(m)

	if input.StoresData != nil {
		storesData = *input.StoresData
	}
	if input.DataSurvivesRestart != nil {
		dataSurvives = *input.DataSurvivesRestart
	}
	if input.DataSharedAcrossInstances != nil {
		dataShared = *input.DataSharedAcrossInstances
	}
	if input.DataLossImpact != nil {
		dataLossImpact = *input.DataLossImpact
	}

	return stateIntent{
		storesData:                storesData,
		dataSurvivesRestart:       dataSurvives,
		dataSharedAcrossInstances: dataShared,
		dataLossImpact:            dataLossImpact,
	}
}

func readCurrentState(m map[string]any) (storesData, dataSurvives, dataShared bool, dataLossImpact string) {
	state, ok := m["state"].(map[string]any)
	if !ok {
		return
	}
	if t, ok := state["type"].(string); ok && t != contract.StateStateless {
		storesData = true
	}
	if p, ok := state["persistence"].(map[string]any); ok {
		if d, ok := p["durability"].(string); ok && d == contract.DurabilityPersistent {
			dataSurvives = true
		}
		if s, ok := p["scope"].(string); ok && s == contract.ScopeShared {
			dataShared = true
		}
	}
	if dc, ok := state["dataCriticality"].(string); ok {
		dataLossImpact = dc
	}
	return
}

// firstConfigSchema returns the schema path of the first configuration that
// declares one, or "".
func firstConfigSchema(m map[string]any) string {
	cfgs, _ := m["configurations"].([]any)
	for _, cfg := range cfgs {
		if cm, ok := cfg.(map[string]any); ok {
			if rel, _ := cm["schema"].(string); rel != "" {
				return rel
			}
		}
	}
	return ""
}

// mergeConfigSchema resolves the schema add_config_properties writes to (the
// first configuration's local schema file) and returns it with props merged in.
// A property already in the schema is replaced; required names are unioned.
func mergeConfigSchema(dir string, m map[string]any, props []ConfigProperty) (string, []byte, error) {
	rel := firstConfigSchema(m)
	if rel == "" || !filepath.IsLocal(rel) {
		return "", nil, fmt.Errorf("add_config_properties needs a configuration with a local schema file, got %q", rel)
	}
	existing, err := os.ReadFile(filepath.Join(dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return rel, generateConfigSchema(props), nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to read %s: %w", rel, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(existing, &schema); err != nil || schema == nil {
		return "", nil, fmt.Errorf("configuration schema %s is not a JSON object", rel)
	}
	return rel, mergeSchemaProperties(schema, props), nil
}

// mergeSchemaProperties adds props to a decoded schema object and re-encodes it.
func mergeSchemaProperties(schema map[string]any, props []ConfigProperty) []byte {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	required, _ := schema["required"].([]any)
	have := map[any]bool{}
	for _, r := range required {
		have[r] = true
	}
	fresh := map[string]any{}
	_ = json.Unmarshal(generateConfigSchema(props), &fresh) //nolint:errcheck // generated by us
	for name, def := range fresh["properties"].(map[string]any) {
		properties[name] = def
	}
	for _, p := range props {
		if p.Required && !have[p.Name] {
			required = append(required, p.Name)
			have[p.Name] = true
		}
	}
	schema["properties"] = properties
	if len(required) > 0 {
		schema["required"] = required
	}
	data, _ := json.MarshalIndent(schema, "", "  ") //nolint:errcheck // decoded JSON always re-encodes
	return append(data, '\n')
}

func ensureConfigSection(m map[string]any) {
	if _, ok := m["configurations"]; ok {
		return
	}
	m["configurations"] = []any{
		map[string]any{
			"name":     "default",
			"schema":   "configuration/schema.json",
			"required": true,
		},
	}
}

func applyMetadataEdits(m map[string]any, set map[string]any, remove []string) []string {
	var changes []string
	meta, ok := m["metadata"].(map[string]any)
	if !ok {
		meta = make(map[string]any)
	}

	for k, v := range set {
		meta[k] = v
		changes = append(changes, fmt.Sprintf("set metadata %q", k))
	}

	for _, k := range remove {
		delete(meta, k)
		changes = append(changes, fmt.Sprintf("removed metadata %q", k))
	}

	if len(meta) > 0 {
		m["metadata"] = meta
	} else {
		delete(m, "metadata")
	}

	return changes
}

// --- Check ---

// Check validates an existing contract and returns structured results.
//
// The verdict comes from validate, the same path `pacto validate` runs
// (lockfile verification, recursive policy resolution), so an agent looping
// until Check says valid stops on a contract CI also accepts. A nil validate
// falls back to local validation with no policy resolver, which fails a
// ref-based policy closed rather than downgrading it to a warning.
func Check(ctx context.Context, validate Validator, path string) (*CheckResult, error) {
	return osFileOps().check(ctx, validate, path)
}

func (o fileOps) check(ctx context.Context, validate Validator, path string) (*CheckResult, error) {
	dir := path
	if dir == "" {
		dir = "."
	}

	pactoPath := filepath.Join(dir, "pacto.yaml")
	rawYAML, err := os.ReadFile(pactoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pactoPath, err)
	}

	c, parseErr := contract.Parse(bytes.NewReader(rawYAML))
	if parseErr != nil {
		result := &CheckResult{Valid: false}
		result.Errors = []ValidationItem{{
			Path:    "",
			Code:    "PARSE_ERROR",
			Message: parseErr.Error(),
		}}
		return result, nil
	}

	var vr validation.Report
	if validate != nil {
		if vr, err = validate(ctx, dir); err != nil {
			return nil, err
		}
	} else {
		vr.Stages = validation.ValidateStagesWithResolver(ctx, c, rawYAML, o.dirFS(dir), nil)
		vr.ValidationResult = validation.Merged(vr.Stages)
	}

	cr := &CheckResult{
		Valid:   vr.IsValid(),
		Summary: summarizeContract(c),
		Stages:  vr.Stages,
		Next:    vr.Next,
	}

	for _, e := range vr.Errors {
		cr.Errors = append(cr.Errors, ValidationItem{
			Path:    e.Path,
			Code:    e.Code,
			Message: e.Message,
			Line:    e.Line,
			Column:  e.Column,
			File:    e.File,
		})
	}
	for _, w := range vr.Warnings {
		cr.Warnings = append(cr.Warnings, ValidationItem{
			Path:    w.Path,
			Code:    w.Code,
			Message: w.Message,
		})
	}

	cr.Suggestions = buildSuggestions(c, o.dirFS(dir), vr.IsValid())

	return cr, nil
}

// --- YAML marshaling with ordered keys ---

// topLevelKeyOrder is the order marshalContract writes the top-level fields
// in: the newest schema's order.
var topLevelKeyOrder = []string{
	"pactoVersion", "service", "interfaces", "configurations", "environments",
	"policies", "dependencies", "state", "workload", "capabilities", "readiness", "publish", "checks", "metadata", "extensions",
}

// marshalContract writes m with its top-level keys in topLevelKeyOrder, then
// any key that order does not list, sorted, so it never drops one.
func (o fileOps) marshalContract(m map[string]any) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	mapping := &yaml.Node{Kind: yaml.MappingNode}

	keys := slices.Clone(topLevelKeyOrder)
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(topLevelKeyOrder, key) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		val, ok := m[key]
		if !ok {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}

		// Add blank line before major sections
		if key != "pactoVersion" {
			keyNode.HeadComment = ""
		}

		valNode, err := o.valueToNode(val)
		if err != nil {
			return nil, fmt.Errorf("marshaling %s: %w", key, err)
		}
		mapping.Content = append(mapping.Content, keyNode, valNode)
	}

	doc.Content = append(doc.Content, mapping)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(doc) //nolint:errcheck // encoding a valid node tree to a buffer cannot fail
	_ = enc.Close()     //nolint:errcheck // closing a buffer encoder cannot fail
	return buf.Bytes(), nil
}

func (o fileOps) valueToNode(v any) (*yaml.Node, error) {
	data, err := o.marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := o.unmarshal(data, &doc); err != nil {
		return nil, err
	}
	// Normally yaml wraps in a DocumentNode with one content child, but empty or
	// whitespace-only input yields no content — guard against an index panic.
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("yaml produced no content for value %#v", v)
	}
	return doc.Content[0], nil
}

// --- Validation helpers ---

// validateYAML parses and validates a generated contract, returning the parsed
// contract so the caller can summarize it instead of re-deriving a summary from
// the map it was built out of.
func validateYAML(yamlBytes []byte) (*contract.Contract, error) {
	c, err := contract.Parse(bytes.NewReader(yamlBytes))
	if err != nil {
		return nil, err
	}

	bundleFS := buildStubFS(c, yamlBytes)

	result := validation.Validate(c, yamlBytes, bundleFS)
	if !result.IsValid() {
		return nil, fmt.Errorf("%s", result.Errors[0].Message)
	}
	return c, nil
}

// buildStubFS creates an in-memory FS with pacto.yaml and stub files for
// referenced interface contracts and config schema, so validation passes
// before files are written to disk.
//
// It stubs every declared interface ref, which is only honest if every declared
// interface ref is also written. writeBundle and scaffoldNewInterfaceFiles
// therefore scaffold all three interface types too: asyncapi used to be
// stubbed-but-never-written, so pacto_create reported success and left a bundle
// that failed FILE_NOT_FOUND on the very next read.
func buildStubFS(c *contract.Contract, yamlBytes []byte) fstest.MapFS {
	m := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: yamlBytes},
	}
	for _, iface := range c.Interfaces {
		if iface.Ref != "" {
			m[iface.Ref] = &fstest.MapFile{Data: []byte("{}")}
		}
	}
	for _, cfg := range c.Configurations {
		if cfg.Schema != "" {
			m[cfg.Schema] = &fstest.MapFile{Data: []byte(`{"type":"object"}`)}
		}
	}
	return m
}

// buildBundleFSForValidation is the bundle in dir as an edit leaves it: the
// files on disk, then the edited YAML and files over them, then stubs for
// refs that are not on disk yet.
func (o fileOps) buildBundleFSForValidation(dir string, yamlBytes []byte, c *contract.Contract, files map[string][]byte) fstest.MapFS {
	m := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: yamlBytes},
	}

	// Walk the existing directory to include all files
	dirFS := o.dirFS(dir)
	// WalkDir callback always returns nil, so WalkDir itself cannot error.
	_ = fs.WalkDir(dirFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip errors
		}
		if path == "." || path == "pacto.yaml" {
			return nil
		}
		if d.IsDir() {
			m[path] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			return nil
		}
		data, err := fs.ReadFile(dirFS, path)
		if err != nil {
			return nil
		}
		m[path] = &fstest.MapFile{Data: data, Mode: 0644}
		return nil
	})

	for rel, data := range files {
		m[rel] = &fstest.MapFile{Data: data, Mode: 0644}
	}

	// Add stubs for referenced files not yet on disk
	for _, iface := range c.Interfaces {
		if iface.Ref != "" {
			if _, ok := m[iface.Ref]; !ok {
				m[iface.Ref] = &fstest.MapFile{Data: []byte("{}")}
			}
		}
	}
	for _, cfg := range c.Configurations {
		if cfg.Schema != "" {
			if _, ok := m[cfg.Schema]; !ok {
				m[cfg.Schema] = &fstest.MapFile{Data: []byte(`{"type":"object"}`)}
			}
		}
	}

	return m
}

// --- File writing ---

func (o fileOps) writeBundle(dir string, yamlBytes []byte, input CreateInput) (int, error) {
	if err := o.mkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	fileCount := 0

	// Write pacto.yaml
	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := o.atomicWriteFile(pactoPath, yamlBytes, 0644); err != nil {
		return 0, fmt.Errorf("failed to write %s: %w", pactoPath, err)
	}
	fileCount++

	// Scaffold interface spec files
	for _, iface := range input.Interfaces {
		ifaceDir := filepath.Join(dir, "interfaces")
		if err := o.mkdirAll(ifaceDir, 0755); err != nil {
			return fileCount, fmt.Errorf("failed to create %s: %w", ifaceDir, err)
		}
		ifacePath := filepath.Join(ifaceDir, iface.Name+".yaml")
		stub := scaffoldInterfaceStub(input.Name, iface)
		if err := o.atomicWriteFile(ifacePath, stub, 0644); err != nil {
			return fileCount, fmt.Errorf("failed to write %s: %w", ifacePath, err)
		}
		fileCount++
	}

	// Scaffold config schema if needed
	if len(input.ConfigProperties) > 0 {
		configDir := filepath.Join(dir, "configuration")
		if err := o.mkdirAll(configDir, 0755); err != nil {
			return fileCount, fmt.Errorf("failed to create %s: %w", configDir, err)
		}
		schemaPath := filepath.Join(configDir, "schema.json")
		schema := generateConfigSchema(input.ConfigProperties)
		if err := o.atomicWriteFile(schemaPath, schema, 0644); err != nil {
			return fileCount, fmt.Errorf("failed to write %s: %w", schemaPath, err)
		}
		fileCount++
	}

	return fileCount, nil
}

func (o fileOps) scaffoldNewInterfaceFiles(dir string, interfaces []InterfaceInput) error {
	for _, iface := range interfaces {
		ifaceDir := filepath.Join(dir, "interfaces")
		if err := o.mkdirAll(ifaceDir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", ifaceDir, err)
		}
		ifacePath := filepath.Join(ifaceDir, iface.Name+".yaml")
		// Only write if file doesn't exist
		if _, err := os.Stat(ifacePath); err == nil {
			continue
		}
		stub := scaffoldInterfaceStub("service", iface)
		if err := o.atomicWriteFile(ifacePath, stub, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", ifacePath, err)
		}
	}
	return nil
}

// scaffoldInterfaceStub renders a starter spec for one declared interface. It
// covers all three interface types because every declared interface is written:
// a type with no stub of its own would still get a file, and an OpenAPI document
// under an asyncapi interface is worse than an empty one.
func scaffoldInterfaceStub(serviceName string, iface InterfaceInput) []byte {
	switch iface.Type {
	case contract.InterfaceTypeGRPC:
		capitalizedName := strings.ToUpper(serviceName[:1]) + serviceName[1:]
		return []byte(fmt.Sprintf(`syntax = "proto3";

package %s;

service %sService {
  // Add your RPC methods here
}
`, serviceName, capitalizedName))
	case contract.InterfaceTypeAsyncAPI:
		return []byte(fmt.Sprintf(`asyncapi: "3.0.0"
info:
  title: %s
  version: "0.1.0"
channels: {}
operations: {}
`, serviceName))
	}

	// Default: OpenAPI stub
	return []byte(fmt.Sprintf(`openapi: "3.0.0"
info:
  title: %s
  version: "0.1.0"
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
`, serviceName))
}

func generateConfigSchema(props []ConfigProperty) []byte {
	properties := make(map[string]any)
	var required []string

	for _, p := range props {
		propType := p.Type
		if propType == "" {
			propType = "string"
		}
		properties[p.Name] = map[string]any{
			"type": propType,
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	data, _ := json.MarshalIndent(schema, "", "  ")
	return append(data, '\n')
}

// atomicWriteFile writes data to a uniquely named temp file next to path and
// renames it into place, so concurrent writers never share a temp file and a
// reader never sees a partial one. An existing file keeps its permissions; the
// temp file is removed on any failure.
func (o fileOps) atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := o.stageFile(path, data, perm)
	if err != nil {
		return err
	}
	if err = o.rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// stageFile writes data to a uniquely named temp file next to path, with the
// permissions path has when it exists, and returns the temp file's name. The
// temp file is removed on any failure.
func (o fileOps) stageFile(path string, data []byte, perm os.FileMode) (string, error) {
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pacto-tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_ = f.Close()
	err = o.writeFile(tmp, data, perm)
	if err == nil {
		// CreateTemp opens 0600 and WriteFile keeps an existing file's mode.
		err = os.Chmod(tmp, perm)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeFiles writes every file under dir in one sequence: each goes to a temp
// file beside its target first, and only once all are written does each
// replace its target, pacto.yaml last, so a failed write changes nothing.
func (o fileOps) writeFiles(dir string, files map[string][]byte) error {
	names := slices.DeleteFunc(slices.Sorted(maps.Keys(files)), func(rel string) bool { return rel == "pacto.yaml" })
	names = append(names, "pacto.yaml")
	var temps []string
	defer func() {
		for _, tmp := range temps {
			_ = os.Remove(tmp) // gone already once renamed
		}
	}()
	for _, rel := range names {
		path := filepath.Join(dir, rel)
		if err := o.mkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
		}
		tmp, err := o.stageFile(path, files[rel], 0644)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		temps = append(temps, tmp)
	}
	for i, rel := range names {
		if err := o.rename(temps[i], filepath.Join(dir, rel)); err != nil {
			return fmt.Errorf("failed to write %s: %w", filepath.Join(dir, rel), err)
		}
	}
	return nil
}

// --- Contract summary ---

func summarizeContract(c *contract.Contract) ContractSummary {
	s := ContractSummary{
		Name:     c.Service.Name,
		Version:  c.Service.Version,
		Owner:    c.Service.Owner.DisplayString(),
		Domain:   c.Service.Domain,
		Offering: c.Service.Offering,
		Summary:  c.Service.Summary,
		Workload: c.Workload,
		Sections: assessSections(c),
	}

	if c.State != nil {
		s.StateType = c.State.Type
	}

	for _, iface := range c.Interfaces {
		s.Interfaces = append(s.Interfaces, summarizeInterface(iface))
	}

	for _, dep := range c.Dependencies {
		ref := dep.Ref
		if ref == "" {
			ref = dep.Name + " (external " + dep.Version + ")"
		}
		if reqs := summarizeRequires(dep.Requires); reqs != "" {
			ref += " (" + reqs + ")"
		}
		s.Dependencies = append(s.Dependencies, ref)
	}

	for _, env := range c.Environments {
		s.Environments = append(s.Environments, env.Name+" ("+env.File+")")
	}

	return s
}

// summarizeInterface is "name (type, lifecycle, compatibility epoch N,
// guarantees: ...)", leaving out what is not set.
func summarizeInterface(iface contract.Interface) string {
	parts := []string{iface.Type}
	if iface.Lifecycle != nil {
		parts = append(parts, iface.Lifecycle.String())
	}
	if iface.CompatibilityEpoch != 0 {
		parts = append(parts, fmt.Sprintf("compatibility epoch %d", iface.CompatibilityEpoch))
	}
	if g := iface.Guarantees; g != nil && g.Text(false) != "" {
		parts = append(parts, "guarantees: "+g.Text(false))
	}
	return iface.Name + " (" + strings.Join(parts, ", ") + ")"
}

// summarizeRequires is what a dependency requires, as declared:
// "requires from http: idempotent; requires from admin".
func summarizeRequires(reqs []contract.Requirement) string {
	parts := make([]string, 0, len(reqs))
	for _, r := range reqs {
		parts = append(parts, "requires from "+r.String())
	}
	return strings.Join(parts, "; ")
}

func assessSections(c *contract.Contract) map[string]string {
	sections := map[string]string{
		"service": "present",
	}

	if len(c.Interfaces) > 0 {
		sections["interfaces"] = "present"
	} else {
		sections["interfaces"] = "absent"
	}

	if c.Workload != "" {
		sections["workload"] = "present"
	} else {
		sections["workload"] = "absent"
	}

	if c.State != nil {
		sections["state"] = "present"
	} else {
		sections["state"] = "absent"
	}

	if len(c.Configurations) > 0 {
		sections["configurations"] = "present"
	} else {
		sections["configurations"] = "absent"
	}
	if len(c.Dependencies) > 0 {
		sections["dependencies"] = "present"
	} else {
		sections["dependencies"] = "absent"
	}
	if len(c.Metadata) > 0 {
		sections["metadata"] = "present"
	} else {
		sections["metadata"] = "absent"
	}
	if len(c.Policies) > 0 {
		sections["policies"] = "present"
	} else {
		sections["policies"] = "absent"
	}

	return sections
}

// --- Suggestions ---

// buildSuggestions is what to improve in a valid contract c, whose bundle
// files bundleFS holds.
func buildSuggestions(c *contract.Contract, bundleFS fs.FS, valid bool) []Suggestion {
	var suggestions []Suggestion

	if !valid {
		return nil
	}

	if len(c.Interfaces) == 0 {
		suggestions = append(suggestions, Suggestion{
			Message: "No interfaces defined. Consider adding an OpenAPI or gRPC interface.",
			ToolCall: &ToolCall{
				Tool:   "pacto_edit",
				Params: map[string]any{"add_interfaces": []map[string]any{{"name": "http-api", "type": "openapi"}}},
			},
		})
	}

	if c.State == nil {
		suggestions = append(suggestions, Suggestion{
			Message: "No state section. The state section describes data semantics.",
		})
	}

	if len(c.Configurations) == 0 {
		suggestions = append(suggestions, Suggestion{
			Message: "No configuration defined. Add a JSON Schema to document config requirements.",
		})
	}

	if len(c.Dependencies) == 0 {
		suggestions = append(suggestions, Suggestion{
			Message: "No dependencies declared. If this service depends on others, declare them explicitly.",
		})
	}

	if c.Service.Domain == "" {
		suggestions = append(suggestions, Suggestion{
			Message:  "No business domain declared. Set service.domain to group this service with its domain in the operational graph.",
			ToolCall: &ToolCall{Tool: "pacto_edit", Params: map[string]any{"domain": "<domain>"}},
		})
	}

	if providerShaped(c) {
		suggestions = append(suggestions, Suggestion{
			Message:  "No platform offering declared. If other teams consume this service as a platform offering, set service.offering and service.summary so they can find it.",
			ToolCall: &ToolCall{Tool: "pacto_edit", Params: map[string]any{"offering": "<offering>", "summary": "<one line>"}},
		})
	}

	if team := c.Service.Owner.Team; pactify.OwnerHint(team) != "" {
		suggestions = append(suggestions, Suggestion{
			Message:  pactify.OwnerHint(team) + ".",
			ToolCall: &ToolCall{Tool: "pacto_edit", Params: map[string]any{"owner": team[strings.Index(team, "/")+1:]}},
		})
	}

	suggestions = append(suggestions, refValuesSuggestions(c, bundleFS)...)

	for _, d := range c.Dependencies {
		if d.External && d.Ref == "" {
			suggestions = append(suggestions, Suggestion{
				Message: fmt.Sprintf("Dependency %q is external with no ref, so Pacto can never confirm its compatibility. "+
					"If your company runs it with Pacto and its contract is not published yet, give ref as the OCI repository "+
					"its contract will be published at and compatibility as the versions you accept, and drop external and version: "+
					"Pacto then reports it as unresolved until it is published. Keep it as it is for a service outside your Pacto system, such as a SaaS API or a model.", d.Name),
			})
		}
	}

	return suggestions
}

// refValuesSuggestions suggests values for each required configuration that
// takes its schema from a referenced contract and has no values, inline or in
// any environment. With no environment declared the call adds one first.
func refValuesSuggestions(c *contract.Contract, bundleFS fs.FS) []Suggestion {
	var out []Suggestion
	given := validation.EnvironmentValueNames(c, bundleFS)
	for _, cfg := range c.Configurations {
		if cfg.Ref == "" || !cfg.Required || len(cfg.Values) > 0 || given[cfg.Name] {
			continue
		}
		set := map[string]any{"environment": "<environment>", "configuration": cfg.Name, "values": map[string]any{}}
		params := map[string]any{"set_environment_values": []map[string]any{set}}
		if len(c.Environments) == 0 {
			params["add_environments"] = []map[string]any{{"name": "<environment>"}}
		} else {
			set["environment"] = c.Environments[0].Name
		}
		out = append(out, Suggestion{
			Message: fmt.Sprintf("Required configuration %q takes its schema from %s and no environment gives values for it. "+
				"Give each environment the values the referenced contract's schema lets this service control.", cfg.Name, cfg.Ref),
			ToolCall: &ToolCall{Tool: "pacto_edit", Params: params},
		})
	}
	return out
}

// providerShaped reports whether c declares no offering yet looks like one:
// no interfaces and no dependencies, but a workload, a configuration or a
// policy, as a database or a shared configuration bundle does.
func providerShaped(c *contract.Contract) bool {
	return c.Service.Offering == "" && len(c.Interfaces) == 0 && len(c.Dependencies) == 0 &&
		(c.Workload != "" || len(c.Configurations) > 0 || len(c.Policies) > 0)
}

// ownerWarnings warns about an owner that is not a bare team name.
func ownerWarnings(team string) []string {
	if hint := pactify.OwnerHint(team); hint != "" {
		return []string{hint}
	}
	return nil
}

// --- Helpers ---

func defaultVersion(v string) string {
	if v == "" {
		return "0.1.0"
	}
	return v
}

func defaultCompatibility(compat string) string {
	if compat != "" {
		return compat
	}
	return "^1.0.0"
}

func collectDerived(input CreateInput, h descriptionHints) []string {
	var derived []string

	if input.Description != "" {
		if h.hasHTTP {
			derived = append(derived, "inferred HTTP interface from description")
		}
		if h.hasGRPC {
			derived = append(derived, "inferred gRPC interface from description")
		}
		if h.hasEvents {
			derived = append(derived, "inferred event interface from description")
		}
		if h.isWorker {
			derived = append(derived, "inferred job workload from description")
		}
		if h.isScheduled {
			derived = append(derived, "inferred scheduled workload from description")
		}
	}

	return derived
}
