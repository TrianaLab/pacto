/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"disabled skips every check", func(c *Config) { *c = Config{} }, ""},
		{"empty image", func(c *Config) { c.Image = "" }, "no image set"},
		{"latest tag", func(c *Config) { c.Image = "ghcr.io/trianalab/pacto/operator:latest" }, "'latest'"},
		{"no tag", func(c *Config) { c.Image = "localhost:5001/pacto/operator" }, "'latest'"},
		{"no namespace", func(c *Config) { c.Namespace = "" }, "namespace"},
		{"operator namespace", func(c *Config) { c.Namespace = c.OperatorNamespace }, "must not be the operator namespace"},
		{"zero interval", func(c *Config) { c.PublishInterval = 0 }, "publish interval"},
		{"sub-second interval", func(c *Config) { c.PublishInterval = 500 * time.Millisecond }, "at least 1s"},
		{"fractional interval", func(c *Config) { c.PublishInterval = 1500 * time.Millisecond }, "whole number of seconds"},
		{"one second", func(c *Config) { c.PublishInterval = time.Second }, ""},
		{"bad quantity", func(c *Config) { c.Resources.CPURequest = "lots" }, "invalid node agent cpu-request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := enabledCfg()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestBuildResources_DefaultsAndOverrides(t *testing.T) {
	def := ResourcesConfig{}.Build(DefaultResources())
	if def.Requests.Cpu().String() != "50m" || def.Limits.Cpu().String() != "200m" ||
		def.Requests.Memory().String() != "64Mi" || def.Limits.Memory().String() != "256Mi" {
		t.Fatalf("unexpected defaults: %+v", def)
	}
	got := ResourcesConfig{CPURequest: "10m", CPULimit: "1", MemoryRequest: "32Mi", MemoryLimit: "1Gi"}.Build(DefaultResources())
	for name, want := range map[corev1.ResourceName][2]string{
		corev1.ResourceCPU:    {"10m", "1"},
		corev1.ResourceMemory: {"32Mi", "1Gi"},
	} {
		req, lim := got.Requests[name], got.Limits[name]
		if req.String() != want[0] || lim.String() != want[1] {
			t.Errorf("%s: got %s/%s, want %s/%s", name, req.String(), lim.String(), want[0], want[1])
		}
	}
}

func enabledCfg() Config {
	return Config{
		Enabled:           true,
		Image:             "ghcr.io/trianalab/pacto/dashboard:3.5.0",
		Namespace:         "test-ns",
		OperatorNamespace: "op-ns",
		PublishInterval:   30 * time.Second,
	}
}
