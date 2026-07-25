package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/validation"
	"gopkg.in/yaml.v3"
)

// Function variables for filesystem operations, overridable in tests.
var (
	osStat      = os.Stat
	osReadFile  = os.ReadFile
	osMkdirAll  = os.MkdirAll
	osWriteFile = os.WriteFile
	osRename    = os.Rename

	yamlMarshalFn   = yaml.Marshal
	yamlUnmarshalFn = yaml.Unmarshal
	osDirFS         = os.DirFS
)

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

	Workload                  string
	StoresData                bool
	DataSurvivesRestart       bool
	DataSharedAcrossInstances bool
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
}

// CheckResult holds the result of a check operation.
type CheckResult struct {
	Valid       bool             `json:"valid"`
	Summary     ContractSummary  `json:"summary"`
	Errors      []ValidationItem `json:"errors,omitempty"`
	Warnings    []ValidationItem `json:"warnings,omitempty"`
	Suggestions []Suggestion     `json:"suggestions,omitempty"`
}

// ContractSummary provides a high-level overview of a contract.
type ContractSummary struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Owner        string            `json:"owner,omitempty"`
	Interfaces   []string          `json:"interfaces,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty"`
	Workload     string            `json:"workload,omitempty"`
	StateType    string            `json:"stateType,omitempty"`
	Sections     map[string]string `json:"sections"`
}

// ValidationItem represents a single validation error or warning.
type ValidationItem struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
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
	storesData  bool
	dataDurable bool
	dataShared  bool
	isWorker    bool
	isScheduled bool
}

var descriptionPatterns = []struct {
	pattern *regexp.Regexp
	apply   func(*descriptionHints)
}{
	{regexp.MustCompile(`(?i)\b(REST|HTTP|API|web)\b`), func(h *descriptionHints) { h.hasHTTP = true }},
	{regexp.MustCompile(`(?i)\bgRPC\b`), func(h *descriptionHints) { h.hasGRPC = true }},
	{regexp.MustCompile(`(?i)\b(event|kafka|nats|pubsub|queue|message|rabbit)\b`), func(h *descriptionHints) { h.hasEvents = true }},
	{regexp.MustCompile(`(?i)\b(postgres|mysql|database|db|mongo|dynamo|cockroach|sql)\b`), func(h *descriptionHints) { h.storesData = true; h.dataDurable = true; h.dataShared = true }},
	{regexp.MustCompile(`(?i)\b(redis|cache|memcache)\b`), func(h *descriptionHints) { h.storesData = true }},
	{regexp.MustCompile(`(?i)\b(worker|consumer|processor)\b`), func(h *descriptionHints) { h.isWorker = true }},
	{regexp.MustCompile(`(?i)\b(cron|scheduled|periodic|batch)\b`), func(h *descriptionHints) { h.isScheduled = true }},
	{regexp.MustCompile(`(?i)\b(stateful|persistent|stores? data|durable)\b`), func(h *descriptionHints) { h.storesData = true; h.dataDurable = true }},
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
func Create(input CreateInput) (*CreateResult, error) {
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
	yamlBytes, err := marshalContract(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contract: %w", err)
	}

	// Validate before writing
	if err := validateYAML(yamlBytes); err != nil {
		return nil, fmt.Errorf("generated contract is invalid: %w", err)
	}

	derived := collectDerived(input, hints)

	if input.DryRun {
		return &CreateResult{
			Path:      filepath.Join(dir, "pacto.yaml"),
			Summary:   summarizeFromMap(m),
			Derived:   derived,
			FileCount: 0,
		}, nil
	}

	// Write files
	fileCount, err := writeBundle(dir, yamlBytes, input)
	if err != nil {
		return nil, err
	}

	return &CreateResult{
		Path:      filepath.Join(dir, "pacto.yaml"),
		Summary:   summarizeFromMap(m),
		Derived:   derived,
		FileCount: fileCount,
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

	if !input.StoresData && h.storesData {
		input.StoresData = true
	}
	if !input.DataSurvivesRestart && h.dataDurable {
		input.DataSurvivesRestart = true
	}
	if !input.DataSharedAcrossInstances && h.dataShared {
		input.DataSharedAcrossInstances = true
	}

	if input.Workload == "" {
		if h.isScheduled {
			input.Workload = contract.WorkloadScheduled
		} else if h.isWorker {
			input.Workload = contract.WorkloadJob
		}
	}
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
	}

	workload := input.Workload
	if workload == "" {
		workload = contract.WorkloadService
	}
	m["workload"] = workload

	intent := stateIntent{
		storesData:                input.StoresData,
		dataSurvivesRestart:       input.DataSurvivesRestart,
		dataSharedAcrossInstances: input.DataSharedAcrossInstances,
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
	ref := strings.TrimPrefix(dep.Ref, "oci://")
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		ref = ref[:colon]
	}
	return ref[strings.LastIndex(ref, "/")+1:]
}

func buildDependenciesList(inputs []DependencyInput) []any {
	var result []any
	for _, dep := range inputs {
		entry := map[string]any{
			"name":          dependencyName(dep),
			"ref":           dep.Ref,
			"compatibility": defaultCompatibility(dep.Compatibility),
			"required":      dep.Required, // mandatory in v2 schema; emit true AND false
		}
		result = append(result, entry)
	}
	return result
}

// --- Edit ---

// Edit modifies an existing pacto contract.
func Edit(input EditInput) (*EditResult, error) {
	dir := input.Path
	if dir == "" {
		dir = "."
	}

	pactoPath := filepath.Join(dir, "pacto.yaml")
	rawYAML, err := osReadFile(pactoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pactoPath, err)
	}

	var m map[string]any
	if err := yaml.Unmarshal(rawYAML, &m); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", pactoPath, err)
	}

	changes := applyEdits(m, input)

	yamlBytes, err := marshalContract(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contract: %w", err)
	}

	// Validate through Parse first
	c, err := contract.Parse(bytes.NewReader(yamlBytes))
	if err != nil {
		return nil, fmt.Errorf("edited contract is invalid: %w", err)
	}

	// Build FS for validation (existing files + new YAML + stubs for new refs)
	bundleFS := buildBundleFSForValidation(dir, yamlBytes, c)

	result := validation.Validate(c, yamlBytes, bundleFS)
	if !result.IsValid() {
		return nil, fmt.Errorf("edited contract fails validation: %s", result.Errors[0].Message)
	}

	summary := summarizeContract(c)

	if input.DryRun {
		return &EditResult{
			Path:    pactoPath,
			Summary: summary,
			Changes: changes,
		}, nil
	}

	if err := atomicWriteFile(pactoPath, yamlBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", pactoPath, err)
	}

	// Scaffold any new interface contract files
	if err := scaffoldNewInterfaceFiles(dir, input.AddInterfaces); err != nil {
		return nil, err
	}

	return &EditResult{
		Path:    pactoPath,
		Summary: summary,
		Changes: changes,
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
		entry := map[string]any{
			"name":          dependencyName(dep),
			"ref":           dep.Ref,
			"compatibility": defaultCompatibility(dep.Compatibility),
			"required":      dep.Required, // mandatory in v2 schema; emit true AND false
		}
		deps = append(deps, entry)
		changes = append(changes, fmt.Sprintf("added dependency %q", dep.Ref))
	}
	m["dependencies"] = deps
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
func Check(path string) (*CheckResult, error) {
	dir := path
	if dir == "" {
		dir = "."
	}

	pactoPath := filepath.Join(dir, "pacto.yaml")
	rawYAML, err := osReadFile(pactoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pactoPath, err)
	}

	c, parseErr := contract.Parse(bytes.NewReader(rawYAML))

	bundleFS := osDirFS(dir)

	if parseErr != nil {
		result := &CheckResult{Valid: false}
		result.Errors = []ValidationItem{{
			Path:    "",
			Code:    "PARSE_ERROR",
			Message: parseErr.Error(),
		}}
		return result, nil
	}

	vr := validation.Validate(c, rawYAML, bundleFS)

	cr := &CheckResult{
		Valid:   vr.IsValid(),
		Summary: summarizeContract(c),
	}

	for _, e := range vr.Errors {
		cr.Errors = append(cr.Errors, ValidationItem{
			Path:    e.Path,
			Code:    e.Code,
			Message: e.Message,
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

var topLevelKeyOrder = []string{
	"pactoVersion", "service", "interfaces", "configurations",
	"policies", "dependencies", "state", "workload", "capabilities", "readiness", "metadata", "extensions",
}

func marshalContract(m map[string]any) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	mapping := &yaml.Node{Kind: yaml.MappingNode}

	for _, key := range topLevelKeyOrder {
		val, ok := m[key]
		if !ok {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}

		// Add blank line before major sections
		if key != "pactoVersion" {
			keyNode.HeadComment = ""
		}

		valNode, err := valueToNode(val)
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

func valueToNode(v any) (*yaml.Node, error) {
	data, err := yamlMarshalFn(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yamlUnmarshalFn(data, &doc); err != nil {
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

func validateYAML(yamlBytes []byte) error {
	c, err := contract.Parse(bytes.NewReader(yamlBytes))
	if err != nil {
		return err
	}

	bundleFS := buildStubFS(c, yamlBytes)

	result := validation.Validate(c, yamlBytes, bundleFS)
	if !result.IsValid() {
		return fmt.Errorf("%s", result.Errors[0].Message)
	}
	return nil
}

// buildStubFS creates an in-memory FS with pacto.yaml and stub files for
// referenced interface contracts and config schema, so validation passes
// before files are written to disk.
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

func buildBundleFSForValidation(dir string, yamlBytes []byte, c *contract.Contract) fs.FS {
	m := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: yamlBytes},
	}

	// Walk the existing directory to include all files
	dirFS := osDirFS(dir)
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

func writeBundle(dir string, yamlBytes []byte, input CreateInput) (int, error) {
	if err := osMkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	fileCount := 0

	// Write pacto.yaml
	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := atomicWriteFile(pactoPath, yamlBytes, 0644); err != nil {
		return 0, fmt.Errorf("failed to write %s: %w", pactoPath, err)
	}
	fileCount++

	// Scaffold interface spec files
	for _, iface := range input.Interfaces {
		if iface.Type == contract.InterfaceTypeOpenAPI || iface.Type == contract.InterfaceTypeGRPC {
			ifaceDir := filepath.Join(dir, "interfaces")
			if err := osMkdirAll(ifaceDir, 0755); err != nil {
				return fileCount, fmt.Errorf("failed to create %s: %w", ifaceDir, err)
			}
			ifacePath := filepath.Join(ifaceDir, iface.Name+".yaml")
			stub := scaffoldInterfaceStub(input.Name, iface)
			if err := atomicWriteFile(ifacePath, stub, 0644); err != nil {
				return fileCount, fmt.Errorf("failed to write %s: %w", ifacePath, err)
			}
			fileCount++
		}
	}

	// Scaffold config schema if needed
	if len(input.ConfigProperties) > 0 {
		configDir := filepath.Join(dir, "configuration")
		if err := osMkdirAll(configDir, 0755); err != nil {
			return fileCount, fmt.Errorf("failed to create %s: %w", configDir, err)
		}
		schemaPath := filepath.Join(configDir, "schema.json")
		schema := generateConfigSchema(input.ConfigProperties)
		if err := atomicWriteFile(schemaPath, schema, 0644); err != nil {
			return fileCount, fmt.Errorf("failed to write %s: %w", schemaPath, err)
		}
		fileCount++
	}

	return fileCount, nil
}

func scaffoldNewInterfaceFiles(dir string, interfaces []InterfaceInput) error {
	for _, iface := range interfaces {
		if iface.Type != contract.InterfaceTypeOpenAPI && iface.Type != contract.InterfaceTypeGRPC {
			continue
		}
		ifaceDir := filepath.Join(dir, "interfaces")
		if err := osMkdirAll(ifaceDir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", ifaceDir, err)
		}
		ifacePath := filepath.Join(ifaceDir, iface.Name+".yaml")
		// Only write if file doesn't exist
		if _, err := osStat(ifacePath); err == nil {
			continue
		}
		stub := scaffoldInterfaceStub("service", iface)
		if err := atomicWriteFile(ifacePath, stub, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", ifacePath, err)
		}
	}
	return nil
}

func scaffoldInterfaceStub(serviceName string, iface InterfaceInput) []byte {
	if iface.Type == contract.InterfaceTypeGRPC {
		capitalizedName := strings.ToUpper(serviceName[:1]) + serviceName[1:]
		return []byte(fmt.Sprintf(`syntax = "proto3";

package %s;

service %sService {
  // Add your RPC methods here
}
`, serviceName, capitalizedName))
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

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile := filepath.Join(dir, ".pacto-tmp-"+filepath.Base(path))
	if err := osWriteFile(tmpFile, data, perm); err != nil {
		return err
	}
	return osRename(tmpFile, path)
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
		s.Interfaces = append(s.Interfaces, desc)
	}

	for _, dep := range c.Dependencies {
		s.Dependencies = append(s.Dependencies, dep.Ref)
	}

	return s
}

func summarizeFromMap(m map[string]any) ContractSummary {
	s := ContractSummary{Sections: make(map[string]string)}
	summarizeService(&s, m)
	summarizeRuntime(&s, m)
	summarizeInterfacesFromMap(&s, m)
	summarizeDepsFromMap(&s, m)
	summarizeSectionsFromMap(&s, m)
	return s
}

func summarizeService(s *ContractSummary, m map[string]any) {
	svc, ok := m["service"].(map[string]any)
	if !ok {
		return
	}
	s.Name, _ = svc["name"].(string)
	s.Version, _ = svc["version"].(string)
	if str, ok := svc["owner"].(string); ok {
		s.Owner = str
	} else if obj, ok := svc["owner"].(map[string]any); ok {
		if team, _ := obj["team"].(string); team != "" {
			s.Owner = team
		} else if dri, _ := obj["dri"].(string); dri != "" {
			s.Owner = dri
		}
	}
}

func summarizeRuntime(s *ContractSummary, m map[string]any) {
	s.Workload, _ = m["workload"].(string)
	if state, ok := m["state"].(map[string]any); ok {
		s.StateType, _ = state["type"].(string)
	}
}

func summarizeInterfacesFromMap(s *ContractSummary, m map[string]any) {
	ifaces, ok := m["interfaces"].([]any)
	if !ok {
		return
	}
	for _, iface := range ifaces {
		if ifaceMap, ok := iface.(map[string]any); ok {
			name, _ := ifaceMap["name"].(string)
			typ, _ := ifaceMap["type"].(string)
			s.Interfaces = append(s.Interfaces, fmt.Sprintf("%s (%s)", name, typ))
		}
	}
}

func summarizeDepsFromMap(s *ContractSummary, m map[string]any) {
	deps, ok := m["dependencies"].([]any)
	if !ok {
		return
	}
	for _, dep := range deps {
		if depMap, ok := dep.(map[string]any); ok {
			if ref, ok := depMap["ref"].(string); ok {
				s.Dependencies = append(s.Dependencies, ref)
			}
		}
	}
}

func summarizeSectionsFromMap(s *ContractSummary, m map[string]any) {
	for _, key := range []string{"service", "interfaces", "workload", "state"} {
		if _, ok := m[key]; ok {
			s.Sections[key] = "present"
		}
	}
	for _, key := range []string{"policies", "dependencies", "metadata"} {
		if _, ok := m[key]; ok {
			s.Sections[key] = "present"
		} else {
			s.Sections[key] = "absent"
		}
	}
	if _, ok := m["configurations"]; ok {
		s.Sections["configurations"] = "present"
	} else {
		s.Sections["configurations"] = "absent"
	}
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

	return suggestions
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

func intPtr(v int) *int {
	return &v
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
		if h.storesData {
			derived = append(derived, "inferred stateful runtime from description")
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
