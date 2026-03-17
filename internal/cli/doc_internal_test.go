package cli

import (
	"testing"
)

func TestValidateDocFlags(t *testing.T) {
	tests := []struct {
		name    string
		serve   bool
		ui      string
		output  string
		iface   string
		wantErr string
	}{
		{
			name: "all empty is valid",
		},
		{
			name:  "serve alone is valid",
			serve: true,
		},
		{
			name: "ui alone is valid",
			ui:   "swagger",
		},
		{
			name:   "output alone is valid",
			output: "/tmp/out",
		},
		{
			name:  "ui with interface is valid",
			ui:    "swagger",
			iface: "api",
		},
		{
			name:    "serve and ui are mutually exclusive",
			serve:   true,
			ui:      "swagger",
			wantErr: "--serve and --ui are mutually exclusive",
		},
		{
			name:    "serve and output are mutually exclusive",
			serve:   true,
			output:  "/tmp/out",
			wantErr: "--serve/--ui and --output are mutually exclusive",
		},
		{
			name:    "ui and output are mutually exclusive",
			ui:      "swagger",
			output:  "/tmp/out",
			wantErr: "--serve/--ui and --output are mutually exclusive",
		},
		{
			name:    "interface requires ui",
			iface:   "api",
			wantErr: "--interface requires --ui",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDocFlags(tt.serve, tt.ui, tt.output, tt.iface)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error %q, got nil", tt.wantErr)
				return
			}
			if err.Error() != tt.wantErr {
				t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestParseTargets(t *testing.T) {
	tests := []struct {
		name           string
		targets        []string
		wantGlobal     string
		wantInterfaces map[string]string
	}{
		{
			name: "nil targets",
		},
		{
			name:    "empty targets",
			targets: []string{},
		},
		{
			name:       "single global target",
			targets:    []string{"http://localhost:3000"},
			wantGlobal: "http://localhost:3000",
		},
		{
			name:           "single per-interface target",
			targets:        []string{"api=http://localhost:3000"},
			wantInterfaces: map[string]string{"api": "http://localhost:3000"},
		},
		{
			name:           "global and per-interface",
			targets:        []string{"http://global:3000", "admin=http://admin:4000"},
			wantGlobal:     "http://global:3000",
			wantInterfaces: map[string]string{"admin": "http://admin:4000"},
		},
		{
			name:    "multiple per-interface",
			targets: []string{"api=http://api:3000", "admin=http://admin:4000"},
			wantInterfaces: map[string]string{
				"api":   "http://api:3000",
				"admin": "http://admin:4000",
			},
		},
		{
			name:       "last global wins",
			targets:    []string{"http://first:3000", "http://second:4000"},
			wantGlobal: "http://second:4000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global, ifaces := parseTargets(tt.targets)
			if global != tt.wantGlobal {
				t.Errorf("global: expected %q, got %q", tt.wantGlobal, global)
			}
			if tt.wantInterfaces == nil {
				if ifaces != nil {
					t.Errorf("expected nil interfaces, got %v", ifaces)
				}
				return
			}
			if len(ifaces) != len(tt.wantInterfaces) {
				t.Fatalf("expected %d interfaces, got %d: %v", len(tt.wantInterfaces), len(ifaces), ifaces)
			}
			for k, v := range tt.wantInterfaces {
				if ifaces[k] != v {
					t.Errorf("interface %q: expected %q, got %q", k, v, ifaces[k])
				}
			}
		})
	}
}
