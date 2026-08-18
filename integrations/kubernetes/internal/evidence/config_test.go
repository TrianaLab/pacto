/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"testing"
)

// testSubject is an exact, immutable contract revision — the only shape a
// subject may take.
const testSubject = "oci://ghcr.io/acme/payments@sha256:" +
	"1111111111111111111111111111111111111111111111111111111111111111"

func validEnabledConfig() Config {
	return Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "pacto-system",
		Subjects:    []string{testSubject},
		TrustSecret: "trusted-keys",
	}
}

func TestConfigValidate(t *testing.T) {
	noSubjects := validEnabledConfig()
	noSubjects.Subjects = nil

	withCredentials := validEnabledConfig()
	withCredentials.CredentialsSecret = "registry-creds"

	badResources := validEnabledConfig()
	badResources.Resources = ResourcesConfig{MemoryRequest: "not-a-quantity"}

	noImage := validEnabledConfig()
	noImage.Image = ""

	latest := validEnabledConfig()
	latest.Image = "ghcr.io/trianalab/pacto:latest"

	noNamespace := validEnabledConfig()
	noNamespace.Namespace = ""

	noTrust := validEnabledConfig()
	noTrust.TrustSecret = ""

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
		errMsg  string
	}{
		{"disabled is always valid", Config{Enabled: false}, false, ""},
		{"disabled with empty image is valid", Config{Enabled: false, Image: ""}, false, ""},
		{"enabled without image fails", noImage, true, "must be set at build time"},
		{"enabled with latest tag fails", latest, true, "must not use 'latest'"},
		{"enabled without namespace fails", noNamespace, true, "namespace must be set"},
		{"enabled without trust secret fails", noTrust, true, "no trust secret set"},
		// The registry is the store, so an Evidence Server with no subject has
		// nowhere to write: that is a configuration error, not an empty store.
		{"enabled without a subject fails", noSubjects, true, "no contract subject set"},
		{"enabled with bad resource quantity fails", badResources, true, "invalid evidence memory-request quantity"},
		{"enabled with a subject valid", validEnabledConfig(), false, ""},
		{"enabled with registry credentials valid", withCredentials, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResourcesConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		rc      ResourcesConfig
		wantErr bool
	}{
		{"all empty valid", ResourcesConfig{}, false},
		{"all set valid", ResourcesConfig{CPURequest: "25m", CPULimit: "1", MemoryRequest: "64Mi", MemoryLimit: "256Mi"}, false},
		{"bad cpu request", ResourcesConfig{CPURequest: "bad"}, true},
		{"bad cpu limit", ResourcesConfig{CPULimit: "bad"}, true},
		{"bad memory request", ResourcesConfig{MemoryRequest: "bad"}, true},
		{"bad memory limit", ResourcesConfig{MemoryLimit: "bad"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rc.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultResources(t *testing.T) {
	res := DefaultResources()
	if res.Requests.Cpu().String() != "25m" {
		t.Errorf("expected default CPU request 25m, got %s", res.Requests.Cpu().String())
	}
	if res.Requests.Memory().String() != "64Mi" {
		t.Errorf("expected default memory request 64Mi, got %s", res.Requests.Memory().String())
	}
	if res.Limits.Memory().String() != "256Mi" {
		t.Errorf("expected default memory limit 256Mi, got %s", res.Limits.Memory().String())
	}
}

func TestBuildResources_Defaults(t *testing.T) {
	res := ResourcesConfig{}.BuildResources()
	if res.Requests.Cpu().String() != "25m" {
		t.Errorf("expected default CPU request 25m, got %s", res.Requests.Cpu().String())
	}
	if res.Requests.Memory().String() != "64Mi" {
		t.Errorf("expected default memory request 64Mi, got %s", res.Requests.Memory().String())
	}
	if res.Limits.Memory().String() != "256Mi" {
		t.Errorf("expected default memory limit 256Mi, got %s", res.Limits.Memory().String())
	}
}

func TestBuildResources_AllOverrides(t *testing.T) {
	rc := ResourcesConfig{
		CPURequest:    "100m",
		CPULimit:      "500m",
		MemoryRequest: "128Mi",
		MemoryLimit:   "1Gi",
	}
	res := rc.BuildResources()
	if res.Requests.Cpu().String() != "100m" {
		t.Errorf("expected CPU request 100m, got %s", res.Requests.Cpu().String())
	}
	if res.Limits.Cpu().String() != "500m" {
		t.Errorf("expected CPU limit 500m, got %s", res.Limits.Cpu().String())
	}
	if res.Requests.Memory().String() != "128Mi" {
		t.Errorf("expected memory request 128Mi, got %s", res.Requests.Memory().String())
	}
	if res.Limits.Memory().String() != "1Gi" {
		t.Errorf("expected memory limit 1Gi, got %s", res.Limits.Memory().String())
	}
}

func TestHasLatestTag(t *testing.T) {
	tests := []struct {
		image string
		want  bool
	}{
		{"ghcr.io/trianalab/pacto:0.1.0", false}, // real tag
		{"ghcr.io/trianalab/pacto:latest", true}, // explicit latest
		{"ghcr.io/trianalab/pacto", true},        // no tag
		{"registry:5000/pacto:1.0.0", false},     // tag after registry-port colon
		{"registry:5000/pacto", true},            // no tag, port colon then break
	}
	for _, tt := range tests {
		if got := hasLatestTag(tt.image); got != tt.want {
			t.Errorf("hasLatestTag(%q) = %v, want %v", tt.image, got, tt.want)
		}
	}
}

func TestLabels(t *testing.T) {
	labels := Labels()
	if labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("expected %q=%q, got %q", LabelManagedBy, ManagedByValue, labels[LabelManagedBy])
	}
	if labels[LabelComponent] != ComponentValue {
		t.Errorf("expected %q=%q, got %q", LabelComponent, ComponentValue, labels[LabelComponent])
	}
	if labels[LabelName] != Name {
		t.Errorf("expected %q=%q, got %q", LabelName, Name, labels[LabelName])
	}
}

func TestSelectorLabels(t *testing.T) {
	labels := SelectorLabels()
	if labels[LabelComponent] != ComponentValue {
		t.Errorf("expected %q=%q, got %q", LabelComponent, ComponentValue, labels[LabelComponent])
	}
	if labels[LabelName] != Name {
		t.Errorf("expected %q=%q, got %q", LabelName, Name, labels[LabelName])
	}
	if _, ok := labels[LabelManagedBy]; ok {
		t.Errorf("selector labels should not include %q", LabelManagedBy)
	}
}

// --- shared helpers ---

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
