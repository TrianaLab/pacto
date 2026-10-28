/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// applyConfigurationOverrides creates a shallow copy of the contract with configuration
// overrides merged in. The original contract is never mutated (important for loader cache safety).
// Returns the effective contract, a map of overridden key names per configuration, and any error.
//
// This implements name-based configuration matching rather than reusing the pacto library's
// override.Apply (which operates on raw YAML with index-based --set paths). Name-based matching
// is more appropriate for a declarative CRD where array ordering should not affect behavior.
// The merge semantics (override values win) are aligned with the CLI model.
func applyConfigurationOverrides(c *contract.Contract, overrides *pactov1alpha1.ContractOverrides) (*contract.Contract, map[string][]string, error) {
	if overrides == nil || len(overrides.Configurations) == 0 {
		return c, nil, nil
	}

	// Build lookup of existing configurations by name.
	configByName := make(map[string]int, len(c.Configurations))
	for i, cfg := range c.Configurations {
		configByName[cfg.Name] = i
	}

	// Validate all override names exist before applying any.
	for _, ov := range overrides.Configurations {
		if _, ok := configByName[ov.Name]; !ok {
			return nil, nil, fmt.Errorf("configuration %q not found in contract", ov.Name)
		}
	}

	// Shallow-copy the contract and deep-copy the Configurations slice + Values maps.
	effective := *c
	effective.Configurations = make([]contract.Configuration, len(c.Configurations))
	for i, cfg := range c.Configurations {
		effective.Configurations[i] = cfg
		if cfg.Values != nil {
			effective.Configurations[i].Values = make(map[string]any, len(cfg.Values))
			maps.Copy(effective.Configurations[i].Values, cfg.Values)
		}
	}

	// Merge override values (override wins).
	overriddenKeys := make(map[string][]string, len(overrides.Configurations))
	for _, ov := range overrides.Configurations {
		idx := configByName[ov.Name]
		if effective.Configurations[idx].Values == nil {
			effective.Configurations[idx].Values = make(map[string]any, len(ov.Values))
		}
		keys := make([]string, 0, len(ov.Values))
		for k, v := range ov.Values {
			effective.Configurations[idx].Values[k] = v
			keys = append(keys, k)
		}
		sort.Strings(keys)
		overriddenKeys[ov.Name] = keys
	}

	return &effective, overriddenKeys, nil
}

// applyOverrides returns the load result the rest of the reconcile should use: the
// contract with spec.overrides merged into BOTH halves -- the parsed struct and the
// bytes -- or an error if either half cannot be produced.
//
// Both halves, because the two are read by different consumers and the loader does not
// guarantee they came from the same place: the OCI path takes Contract from the bundle
// and RawYAML from the bundle FS, and can leave the bytes empty entirely. Merging only
// the struct is what let an override reach the status while validation still read the
// original document.
func applyOverrides(loadResult *loader.LoadResult, overrides *pactov1alpha1.ContractOverrides) (loader.LoadResult, map[string][]string, error) {
	effective := *loadResult
	if overrides == nil || len(overrides.Configurations) == 0 {
		return effective, nil, nil
	}
	merged, keys, err := applyConfigurationOverrides(loadResult.Contract, overrides)
	if err != nil {
		return effective, nil, err
	}
	// Validation layers 1 and 3 read the BYTES, not the struct, so the merged contract
	// has to reach them as bytes -- otherwise policy enforcement never sees
	// spec.overrides and an override that violates a policy still reports
	// ContractValid=True.
	mergedYAML, err := patchConfigurationValues(loadResult.RawYAML, overrides)
	if err != nil {
		return effective, nil, err
	}
	effective.Contract, effective.RawYAML = merged, mergedYAML
	return effective, keys, nil
}

// patchConfigurationValues rewrites ONLY the overridden configuration values inside
// the loader's own YAML document and returns the result. Every other byte -- every
// unrelated key, every explicitly-empty value -- survives exactly as the file wrote it.
//
// The obvious alternative, re-marshalling the merged struct, is a fail-open:
// contract.Configuration tags Schema, Ref and Values `omitempty`, so a key that was
// present-but-empty in the file (`values: {}` beside a `ref:`, say) disappears from
// the round trip and layer 1 stops seeing the structural violation it was written to
// catch. A bare `overrides: {}` on the CR was enough to turn Invalid into valid, and
// the GitOps promotion gate reads that verdict.
//
// Every failure here is an error, never a fall back to the unpatched bytes: bytes
// that do not carry the override are exactly the state the audit reported.
func patchConfigurationValues(raw []byte, overrides *pactov1alpha1.ContractOverrides) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("re-reading the contract document: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("contract document is empty")
	}
	configs := mappingValue(doc.Content[0], "configurations")
	if configs == nil || configs.Kind != yaml.SequenceNode {
		return nil, errors.New("contract declares no configurations to override")
	}

	for _, ov := range overrides.Configurations {
		entry := configurationNamed(configs, ov.Name)
		if entry == nil {
			return nil, fmt.Errorf("configuration %q not found in the contract document", ov.Name)
		}
		values := mappingValue(entry, "values")
		switch {
		case values == nil:
			values = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "values"}, values)
		case values.Tag == "!!null":
			// `values:` with nothing under it. The override is what gives it content.
			values.Kind, values.Tag, values.Value, values.Style = yaml.MappingNode, "!!map", "", 0
		case values.Kind != yaml.MappingNode:
			return nil, fmt.Errorf("configuration %q declares a non-mapping values block", ov.Name)
		}
		// Sorted so the patched document is byte-identical run to run; Go map order
		// would otherwise reshuffle newly inserted keys on every reconcile.
		for _, k := range slices.Sorted(maps.Keys(ov.Values)) {
			setMappingValue(values, k, ov.Values[k])
		}
	}
	return yaml.Marshal(&doc)
}

// mappingValue returns the value node for key in a mapping node, or nil. A yaml.v3
// mapping stores Content as alternating key, value pairs.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets key to a string scalar, replacing any existing value. The
// override CRD types values as map[string]string, and applyConfigurationOverrides
// merges those same strings into the struct, so both halves agree on the type: a
// numeric-looking override is a string in the document exactly as it is in the
// struct, and a schema expecting a number rejects both.
func setMappingValue(mapping *yaml.Node, key, value string) {
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = scalar
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, scalar)
}

// configurationNamed returns the configurations[] entry whose name is name, or nil.
func configurationNamed(configs *yaml.Node, name string) *yaml.Node {
	for _, entry := range configs.Content {
		if n := mappingValue(entry, "name"); n != nil && n.Value == name {
			return entry
		}
	}
	return nil
}

// --- Helpers ---
