/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// observeConfigurationsDim observes all declared configurations (spec section 7.7 / B6 Secret + B7 ConfigMap).
// ponytail: metadata-only Secret GET (INV-5); ConfigMap decode+validate+discard; stabilize() for NotFound negatives.
func (o *Observer) observeConfigurationsDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	for _, cfg := range input.Contract.Configurations {
		subj := evidence.SubjectRef{Kind: kindConfiguration, Name: cfg.Name}

		// Find binding for this configuration.
		binding := findConfigBinding(input.ConfigBindings, cfg.Name)
		if binding == nil {
			// No binding -> Unsupported (Unknown) for required; optional configs emit nothing per spec.
			if cfg.Required {
				obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Unsupported, prov)
				observations = append(observations, obs)
			}
			continue
		}

		// Route by binding kind.
		switch binding.Kind {
		case resourceSecret:
			obs, updates := o.observeSecretConfiguration(ctx, input, cfg, binding, subj, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, updates...)
		case resourceConfigMap:
			obs, updates := o.observeConfigMapConfiguration(ctx, input, cfg, binding, subj, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, updates...)
		}
	}

	return observations, windowUpdates
}

// findConfigBinding locates the binding for a given configuration name.
func findConfigBinding(bindings []unversioned.ConfigBinding, cfgName string) *unversioned.ConfigBinding {
	for i := range bindings {
		if bindings[i].Configuration == cfgName {
			return &bindings[i]
		}
	}
	return nil
}

// observeSecretConfiguration observes a Secret-backed configuration (B6 metadata-only). Spec section 7.7.
// ponytail: PartialObjectMetadata GET, no .Data read; presence -> Insufficient; NotFound -> windowed.
func (o *Observer) observeSecretConfiguration(
	ctx context.Context,
	input CollectInput,
	cfg contract.Configuration,
	binding *unversioned.ConfigBinding,
	subj evidence.SubjectRef,
	prov evidence.Provenance,
	now time.Time,
) (evidence.Observation, []ObservationWindowUpdate) {
	// Metadata-only GET (INV-5 — Secret VALUES never enter operator memory).
	partial := &metav1.PartialObjectMetadata{}
	partial.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	err := o.client.Get(ctx, client.ObjectKey{Namespace: input.Namespace, Name: binding.Name}, partial)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error (RBAC-denied / other) -> Failed.
			o.fail("configurations", err)
			obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Failed, prov)
			return obs, nil
		}
		// Secret NotFound -> windowed negative.
		// Beyond the window -> confirmed absent (Present=false, Conformant=false -> CONFIGURATION_ABSENT).
		return windowed(input, kindConfiguration, cfg.Name, evidence.NewConfigurationPresent(subj, false, false, prov), now)
	}

	// Secret present (metadata-only) -> Insufficient (existence established, conformance unverified).
	obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
	return obs, resetWindow(kindConfiguration, cfg.Name)
}

// observeConfigMapConfiguration observes a ConfigMap-backed configuration (B7 decode+validate). Spec section 7.7.
// ponytail: decode key+format, validate against local schema, discard content, emit boolean result only.
func (o *Observer) observeConfigMapConfiguration(
	ctx context.Context,
	input CollectInput,
	cfg contract.Configuration,
	binding *unversioned.ConfigBinding,
	subj evidence.SubjectRef,
	prov evidence.Provenance,
	now time.Time,
) (evidence.Observation, []ObservationWindowUpdate) {
	// GET the ConfigMap.
	cm := &corev1.ConfigMap{}
	err := o.client.Get(ctx, client.ObjectKey{Namespace: input.Namespace, Name: binding.Name}, cm)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			o.fail("configurations", err)
			obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Failed, prov)
			return obs, nil
		}
		// ConfigMap NotFound -> windowed negative (required scope).
		// Beyond the window -> confirmed absent (Present=false, Conformant=false -> CONFIGURATION_ABSENT).
		return windowed(input, kindConfiguration, cfg.Name, evidence.NewConfigurationPresent(subj, false, false, prov), now)
	}

	// ConfigMap exists. Without Key+Format -> Insufficient (existence-only, never full-conformance).
	if binding.Key == "" || binding.Format == "" {
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		return obs, resetWindow(kindConfiguration, cfg.Name)
	}

	// With Key+Format: decode+validate the named key.
	content, ok := cm.Data[binding.Key]
	if !ok {
		// Key missing in ConfigMap -> Insufficient (parse failure).
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		return obs, resetWindow(kindConfiguration, cfg.Name)
	}

	// Parse the content by Format (yaml/json).
	parsed, parseErr := parseConfigContent(content, binding.Format)
	if parseErr != nil {
		// Parse failure -> Insufficient.
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		return obs, resetWindow(kindConfiguration, cfg.Name)
	}

	// Validate parsed doc against the configuration's LOCAL schema.
	conforms, err := validateConfigAgainstSchema(parsed, cfg, input.BundleFS, input.BundleDigest)
	if err != nil {
		// Schema remote/unresolvable -> Insufficient (collector cannot validate).
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		return obs, resetWindow(kindConfiguration, cfg.Name)
	}

	// Discard source content (INV-5). Emit only the boolean result.
	// ConfigMap with key+format, values read+parsed+validated -> (Present, Conformant) per validation result.
	// Conformant=true (satisfied) or Conformant=false -> CONFIGURATION_MISMATCH.
	obs := evidence.NewConfigurationPresent(subj, true, conforms, prov)
	return obs, resetWindow(kindConfiguration, cfg.Name)
}

// parseConfigContent parses a config value by format (yaml|json). Returns the parsed document as any.
// ponytail: yaml/json unmarshal into any, return parsed doc for schema validation.
func parseConfigContent(content, format string) (any, error) {
	var parsed any
	var err error
	switch format {
	case formatYAML:
		err = yaml.Unmarshal([]byte(content), &parsed)
	case "json":
		err = json.Unmarshal([]byte(content), &parsed)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	return parsed, err
}

// validateConfigAgainstSchema validates a parsed config document against the configuration's local schema.
// Returns (conforms, error). error != nil means schema unresolvable (remote ref / not in bundle).
// cfg.Schema is a BUNDLE-RELATIVE PATH, not inline JSON — same reading as pkg/validation/crossfield.go.
// ponytail: reuse pkg/validation compileConfigSchema pattern (santhosh-tekuri/jsonschema/v6), no new dep.
func validateConfigAgainstSchema(parsed any, cfg contract.Configuration, bundleFS fs.FS, bundleDigest string) (bool, error) {
	// Remote ref schema -> cannot resolve -> error (Insufficient).
	if cfg.Ref != "" {
		return false, fmt.Errorf("remote ref schema not resolvable locally")
	}
	if cfg.Schema == "" {
		// No schema -> cannot validate -> error.
		return false, fmt.Errorf("no schema defined")
	}
	// Inline contracts carry no bundle files, so the declared schema path cannot be read.
	if bundleFS == nil {
		return false, fmt.Errorf("no bundle filesystem to read schema %q from", cfg.Schema)
	}

	compiledSchema, err := compiledConfigSchema(bundleFS, bundleDigest, cfg.Schema)
	if err != nil {
		return false, err
	}

	// Validate the parsed doc: a failure is non-conformance, not an error.
	return compiledSchema.Validate(parsed) == nil, nil
}

// schemaCacheSize bounds the compiled configuration-schema cache.
const schemaCacheSize = 256

// schemaCache holds compiled configuration schemas keyed by (bundle digest, schema path). A digest names
// immutable content, so an entry never goes stale; a bundle without a digest is never cached.
// ponytail: the whole map is dropped when full, an LRU if a fleet ever holds more live schemas than the cap.
var schemaCache = struct {
	sync.Mutex
	m map[string]*jsonschema.Schema
}{m: map[string]*jsonschema.Schema{}}

// compiledConfigSchema reads and compiles the schema at path in bundleFS, or returns the cached compilation.
func compiledConfigSchema(bundleFS fs.FS, digest, path string) (*jsonschema.Schema, error) {
	key := digest + "\x00" + path
	if digest != "" {
		schemaCache.Lock()
		cached, ok := schemaCache.m[key]
		schemaCache.Unlock()
		if ok {
			return cached, nil
		}
	}

	schemaData, err := fs.ReadFile(bundleFS, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema %q: %w", path, err)
	}

	// Parse the schema file into a JSON doc (per pkg/validation/crossfield.go compileConfigSchema pattern).
	var schemaDoc any
	if err := json.Unmarshal(schemaData, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	compiler.AddResource("mem:///config-schema.json", schemaDoc) //nolint:errcheck
	compiled, err := compiler.Compile("mem:///config-schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	if digest != "" {
		schemaCache.Lock()
		if len(schemaCache.m) >= schemaCacheSize {
			clear(schemaCache.m)
		}
		schemaCache.m[key] = compiled
		schemaCache.Unlock()
	}
	return compiled, nil
}
