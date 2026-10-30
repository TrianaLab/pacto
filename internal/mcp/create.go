package mcp

import (
	"bytes"
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
}

// osFileOps is the production fileOps.
func osFileOps() fileOps {
	return fileOps{os.MkdirAll, os.WriteFile, os.Rename, os.DirFS, yaml.Marshal, yaml.Unmarshal}
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

	AddInterfaces    []InterfaceInput
	RemoveInterfaces []string
	AddDependencies  []DependencyInput
	RemoveDeps       []string

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
// files, the new YAML, stubs for new refs and a merged config schema.
func (o fileOps) validateEdited(dir string, yamlBytes []byte, c *contract.Contract, schemaRel string, schemaBytes []byte) error {
	bundleFS := o.buildBundleFSForValidation(dir, yamlBytes, c)
	if schemaRel != "" {
		bundleFS[schemaRel] = &fstest.MapFile{Data: schemaBytes}
	}
	if result := validation.Validate(c, yamlBytes, bundleFS); !result.IsValid() {
		return fmt.Errorf("edited contract fails validation: %s", result.Errors[0].Message)
	}
	return nil
}

// Edit modifies an existing pacto contract.
func Edit(input EditInput) (*EditResult, error) { return osFileOps().edit(input) }

func (o fileOps) edit(input EditInput) (*EditResult, error) {
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

	changes := applyEdits(m, input)

	var schemaRel string
	var schemaBytes []byte
	if len(input.AddConfigProperties) > 0 {
		if schemaRel, schemaBytes, err = mergeConfigSchema(dir, m, input.AddConfigProperties); err != nil {
			return nil, err
		}
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

	if err := o.validateEdited(dir, yamlBytes, c, schemaRel, schemaBytes); err != nil {
		return nil, err
	}

	summary := summarizeContract(c)
	var warnings []string
	if input.Owner != nil {
		warnings = ownerWarnings(*input.Owner)
	}

	if input.DryRun {
		return &EditResult{
			Path:     pactoPath,
			Summary:  summary,
			Changes:  changes,
			Warnings: warnings,
		}, nil
	}

	// The schema goes first: a failure then leaves pacto.yaml untouched rather
	// than pointing at a schema that was never written.
	if schemaRel != "" {
		if err := o.writeConfigSchema(filepath.Join(dir, schemaRel), schemaBytes); err != nil {
			return nil, err
		}
	}

	if err := o.atomicWriteFile(pactoPath, yamlBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", pactoPath, err)
	}

	// Scaffold any new interface contract files
	if err := o.scaffoldNewInterfaceFiles(dir, input.AddInterfaces); err != nil {
		return nil, err
	}

	return &EditResult{
		Path:     pactoPath,
		Summary:  summary,
		Changes:  changes,
		Warnings: warnings,
	}, nil
}

func applyEdits(m map[string]any, input EditInput) []string {
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

	return changes
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

// writeConfigSchema writes a merged configuration schema, creating its directory.
func (o fileOps) writeConfigSchema(path string, data []byte) error {
	if err := o.mkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	if err := o.atomicWriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
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

	cr.Suggestions = buildSuggestions(c, vr.IsValid())

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

func (o fileOps) buildBundleFSForValidation(dir string, yamlBytes []byte, c *contract.Contract) fstest.MapFS {
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
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pacto-tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Close()
	err = o.writeFile(tmp, data, perm)
	if err == nil {
		// CreateTemp opens 0600 and WriteFile keeps an existing file's mode.
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = o.rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// --- Contract summary ---

func summarizeContract(c *contract.Contract) ContractSummary {
	s := ContractSummary{
		Name:     c.Service.Name,
		Version:  c.Service.Version,
		Owner:    c.Service.Owner.DisplayString(),
		Workload: c.Workload,
		Sections: assessSections(c),
	}

	if c.State != nil {
		s.StateType = c.State.Type
	}

	for _, iface := range c.Interfaces {
		desc := fmt.Sprintf("%s (%s)", iface.Name, iface.Type)
		if iface.Lifecycle != nil {
			desc = fmt.Sprintf("%s (%s, %s)", iface.Name, iface.Type, iface.Lifecycle)
		}
		s.Interfaces = append(s.Interfaces, desc)
	}

	for _, dep := range c.Dependencies {
		ref := dep.Ref
		if ref == "" {
			ref = dep.Name + " (external " + dep.Version + ")"
		}
		s.Dependencies = append(s.Dependencies, ref)
	}

	for _, env := range c.Environments {
		s.Environments = append(s.Environments, env.Name+" ("+env.File+")")
	}

	return s
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

func buildSuggestions(c *contract.Contract, valid bool) []Suggestion {
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

	if team := c.Service.Owner.Team; pactify.OwnerHint(team) != "" {
		suggestions = append(suggestions, Suggestion{
			Message:  pactify.OwnerHint(team) + ".",
			ToolCall: &ToolCall{Tool: "pacto_edit", Params: map[string]any{"owner": team[strings.Index(team, "/")+1:]}},
		})
	}

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
