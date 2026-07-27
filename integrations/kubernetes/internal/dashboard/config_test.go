/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "disabled config is always valid",
			cfg:     Config{Enabled: false},
			wantErr: false,
		},
		{
			name:    "disabled with empty image is valid",
			cfg:     Config{Enabled: false, Image: ""},
			wantErr: false,
		},
		{
			name:    "enabled without image fails",
			cfg:     Config{Enabled: true, Namespace: "ns"},
			wantErr: true,
			errMsg:  "dashboard image must be set at build time",
		},
		{
			name:    "enabled without namespace fails",
			cfg:     Config{Enabled: true, Image: "ghcr.io/trianalab/pacto-dashboard:0.24.2"},
			wantErr: true,
			errMsg:  "dashboard namespace must be set",
		},
		{
			name:    "enabled with latest tag fails",
			cfg:     Config{Enabled: true, Image: "ghcr.io/trianalab/pacto-dashboard:latest", Namespace: "ns"},
			wantErr: true,
			errMsg:  "must not use 'latest'",
		},
		{
			name:    "enabled with no tag fails (implicit latest)",
			cfg:     Config{Enabled: true, Image: "ghcr.io/trianalab/pacto-dashboard", Namespace: "ns"},
			wantErr: true,
			errMsg:  "must not use 'latest'",
		},
		{
			name: "enabled with valid config succeeds",
			cfg: Config{
				Enabled:   true,
				Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
				Namespace: "pacto-system",
			},
			wantErr: false,
		},
		{
			name: "enabled with OCI secret is valid",
			cfg: Config{
				Enabled:   true,
				Image:     "ghcr.io/trianalab/pacto-dashboard:1.0.0",
				Namespace: "pacto-system",
				OCISecret: "registry-creds",
			},
			wantErr: false,
		},
		{
			name: "enabled with invalid resource quantity fails",
			cfg: Config{
				Enabled:   true,
				Image:     "ghcr.io/trianalab/pacto-dashboard:1.0.0",
				Namespace: "pacto-system",
				Resources: ResourcesConfig{MemoryRequest: "not-a-quantity"},
			},
			wantErr: true,
			errMsg:  "invalid dashboard memory-request quantity",
		},
		{
			name: "enabled with valid resource overrides succeeds",
			cfg: Config{
				Enabled:   true,
				Image:     "ghcr.io/trianalab/pacto-dashboard:1.0.0",
				Namespace: "pacto-system",
				Resources: ResourcesConfig{CPURequest: "100m", CPULimit: "1", MemoryRequest: "256Mi", MemoryLimit: "1Gi"},
			},
			wantErr: false,
		},
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

func TestHasLatestTag(t *testing.T) {
	tests := []struct {
		image string
		want  bool
	}{
		{"ghcr.io/trianalab/pacto-dashboard:0.24.2", false},
		{"ghcr.io/trianalab/pacto-dashboard:latest", true},
		{"ghcr.io/trianalab/pacto-dashboard", true},
		{"my-registry.com/dashboard:v1.2.3", false},
		{"dashboard:1.0", false},
		{"dashboard:latest", true},
		{"dashboard", true},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := hasLatestTag(tt.image)
			if got != tt.want {
				t.Errorf("hasLatestTag(%q) = %v, want %v", tt.image, got, tt.want)
			}
		})
	}
}

func TestEffectiveOCISecrets_OCISecretsOnly(t *testing.T) {
	cfg := Config{OCISecrets: []string{"a", "b"}}
	got := cfg.EffectiveOCISecrets()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}
}

func TestEffectiveOCISecrets_OCISecretFallback(t *testing.T) {
	cfg := Config{OCISecret: "legacy"}
	got := cfg.EffectiveOCISecrets()
	if len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("expected [legacy], got %v", got)
	}
}

func TestEffectiveOCISecrets_OCISecretsPrecedence(t *testing.T) {
	cfg := Config{OCISecret: "old", OCISecrets: []string{"new"}}
	got := cfg.EffectiveOCISecrets()
	if len(got) != 1 || got[0] != "new" {
		t.Fatalf("expected OCISecrets to take precedence, got %v", got)
	}
}

func TestEffectiveOCISecrets_Empty(t *testing.T) {
	cfg := Config{}
	got := cfg.EffectiveOCISecrets()
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestBuildResources_Defaults(t *testing.T) {
	rc := ResourcesConfig{}
	res := rc.BuildResources()
	if res.Requests.Cpu().String() != "50m" {
		t.Errorf("expected default CPU request 50m, got %s", res.Requests.Cpu().String())
	}
	if res.Requests.Memory().String() != "128Mi" {
		t.Errorf("expected default memory request 128Mi, got %s", res.Requests.Memory().String())
	}
	if res.Limits.Memory().String() != "512Mi" {
		t.Errorf("expected default memory limit 512Mi, got %s", res.Limits.Memory().String())
	}
}

func TestBuildResources_AllOverrides(t *testing.T) {
	rc := ResourcesConfig{
		CPURequest:    "100m",
		CPULimit:      "500m",
		MemoryRequest: "256Mi",
		MemoryLimit:   "1Gi",
	}
	res := rc.BuildResources()
	if res.Requests.Cpu().String() != "100m" {
		t.Errorf("expected CPU request 100m, got %s", res.Requests.Cpu().String())
	}
	if res.Limits.Cpu().String() != "500m" {
		t.Errorf("expected CPU limit 500m, got %s", res.Limits.Cpu().String())
	}
	if res.Requests.Memory().String() != "256Mi" {
		t.Errorf("expected memory request 256Mi, got %s", res.Requests.Memory().String())
	}
	if res.Limits.Memory().String() != "1Gi" {
		t.Errorf("expected memory limit 1Gi, got %s", res.Limits.Memory().String())
	}
}

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
