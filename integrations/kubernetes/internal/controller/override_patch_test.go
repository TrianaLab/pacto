/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"strings"
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

// overrideOf is the one-scope override these tests keep reaching for.
func overrideOf(name string, kv map[string]string) *pactov1alpha1.ContractOverrides {
	return &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{{Name: name, Values: kv}},
	}
}

// ---------- patchConfigurationValues: what it must preserve ----------

// The whole reason this function patches the document instead of re-marshalling the
// merged struct. `values: {}` beside a `ref:` is a structural violation that layer 1
// exists to catch, and contract.Configuration's `omitempty` tag drops it on a struct
// round trip -- so overriding a DIFFERENT scope used to launder the violation away.
func TestPatchConfigurationValues_KeepsAnUntouchedScopeByteForByte(t *testing.T) {
	const raw = `pactoVersion: "2.0"
service:
  name: test-svc
configurations:
  - name: broken
    ref: oci://registry.example.com/config:1.0.0
    values: {}
  - name: app
    schema: configuration/schema.json
    values:
      logLevel: info
`
	out, err := patchConfigurationValues([]byte(raw), overrideOf("app", map[string]string{"logLevel": "debug"}))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "values: {}") {
		t.Errorf("the untouched scope lost its empty values block, so layer 1 can no longer see the violation:\n%s", got)
	}
	if !strings.Contains(got, "ref: oci://registry.example.com/config:1.0.0") {
		t.Errorf("the untouched scope lost its ref:\n%s", got)
	}
	if !strings.Contains(got, "logLevel: debug") {
		t.Errorf("the override did not land:\n%s", got)
	}
}

// Two runs must produce identical bytes. Go map iteration order is randomised, so an
// unsorted write would reshuffle newly inserted keys and make every reconcile look
// like a change.
func TestPatchConfigurationValues_IsDeterministic(t *testing.T) {
	const raw = `configurations:
  - name: app
    values:
      a: "1"
`
	ov := overrideOf("app", map[string]string{"z": "26", "m": "13", "b": "2", "y": "25"})
	first, err := patchConfigurationValues([]byte(raw), ov)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	for i := range 20 {
		next, err := patchConfigurationValues([]byte(raw), ov)
		if err != nil {
			t.Fatalf("patch %d: %v", i, err)
		}
		if string(next) != string(first) {
			t.Fatalf("run %d differs:\n--- first ---\n%s\n--- next ---\n%s", i, first, next)
		}
	}
}

// ---------- patchConfigurationValues: the shapes a values block arrives in ----------

func TestPatchConfigurationValues_ValuesBlockShapes(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		kv    map[string]string
		wants []string
	}{
		{
			name: "no values key at all -- one is created",
			raw: `configurations:
  - name: app
    schema: configuration/schema.json
`,
			kv:    map[string]string{"logLevel": "debug"},
			wants: []string{"logLevel: debug"},
		},
		{
			name: "an explicitly null values key -- the override gives it content",
			raw: `configurations:
  - name: app
    values:
`,
			kv:    map[string]string{"logLevel": "debug"},
			wants: []string{"logLevel: debug"},
		},
		{
			name: "an existing key is replaced, a new one appended",
			raw: `configurations:
  - name: app
    values:
      logLevel: info
`,
			kv:    map[string]string{"logLevel": "debug", "region": "eu"},
			wants: []string{"logLevel: debug", "region: eu"},
		},
		{
			name: "a numeric-looking override stays a string, exactly as the struct merge leaves it",
			raw: `configurations:
  - name: app
    values:
      port: "8080"
`,
			kv:    map[string]string{"port": "9090"},
			wants: []string{`port: "9090"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := patchConfigurationValues([]byte(tt.raw), overrideOf("app", tt.kv))
			if err != nil {
				t.Fatalf("patch: %v", err)
			}
			for _, want := range tt.wants {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			if strings.Contains(string(out), "logLevel: info") {
				t.Errorf("the old value survived:\n%s", out)
			}
		})
	}
}

// ---------- patchConfigurationValues: every failure is an error ----------

// Never a fall back to the unpatched bytes: bytes that do not carry the override are
// exactly the fail-open this function was written to close.
func TestPatchConfigurationValues_Errors(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		scope   string
		wantErr string
	}{
		{
			name:    "bytes that are not YAML",
			raw:     "\tthis: [is not: yaml",
			scope:   "app",
			wantErr: "re-reading the contract document",
		},
		{
			name:    "no document at all -- the OCI loader leaves RawYAML empty when the bundle FS has no pacto.yaml",
			raw:     "",
			scope:   "app",
			wantErr: "contract document is empty",
		},
		{
			name:    "a document whose root is not a mapping",
			raw:     "- just\n- a list\n",
			scope:   "app",
			wantErr: "declares no configurations",
		},
		{
			name:    "a document with no configurations key",
			raw:     "service:\n  name: test-svc\n",
			scope:   "app",
			wantErr: "declares no configurations",
		},
		{
			name:    "a configurations key that is not a sequence",
			raw:     "configurations: nope\n",
			scope:   "app",
			wantErr: "declares no configurations",
		},
		{
			name:    "a scope the document does not declare",
			raw:     "configurations:\n  - name: app\n",
			scope:   "nope",
			wantErr: `configuration "nope" not found`,
		},
		{
			name:    "an entry with no name at all is not a match",
			raw:     "configurations:\n  - schema: configuration/schema.json\n",
			scope:   "app",
			wantErr: `configuration "app" not found`,
		},
		{
			name:    "a values block that is not a mapping",
			raw:     "configurations:\n  - name: app\n    values: nope\n",
			scope:   "app",
			wantErr: "non-mapping values block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := patchConfigurationValues([]byte(tt.raw), overrideOf(tt.scope, map[string]string{"k": "v"}))
			if err == nil {
				t.Fatalf("expected an error, got bytes:\n%s", out)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
			if out != nil {
				t.Errorf("a failed patch must return no bytes, got:\n%s", out)
			}
		})
	}
}
