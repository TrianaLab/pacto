package contract_test

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestParseRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"caret", "^2.0.0", false},
		{"tilde", "~1.0.0", false},
		{"exact", "1.2.3", false},
		{"range", ">= 1.0.0, < 2.0.0", false},
		{"invalid", "not-a-range", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := contract.ParseRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestRangeContains(t *testing.T) {
	tests := []struct {
		rangeStr string
		version  string
		want     bool
	}{
		{"^2.0.0", "2.0.0", true},
		{"^2.0.0", "2.1.0", true},
		{"^2.0.0", "2.99.99", true},
		{"^2.0.0", "3.0.0", false},
		{"^2.0.0", "1.9.9", false},
		{"~1.2.0", "1.2.0", true},
		{"~1.2.0", "1.2.5", true},
		{"~1.2.0", "1.3.0", false},
		{">= 1.0.0, < 2.0.0", "1.5.0", true},
		{">= 1.0.0, < 2.0.0", "2.0.0", false},
		{"^1.0.0", "not-a-version", false},
	}

	for _, tt := range tests {
		t.Run(tt.rangeStr+"_"+tt.version, func(t *testing.T) {
			r, err := contract.ParseRange(tt.rangeStr)
			if err != nil {
				t.Fatalf("ParseRange(%q) failed: %v", tt.rangeStr, err)
			}
			if got := r.Contains(tt.version); got != tt.want {
				t.Errorf("Range(%q).Contains(%q) = %v, want %v", tt.rangeStr, tt.version, got, tt.want)
			}
		})
	}
}

func TestRange_String(t *testing.T) {
	r, err := contract.ParseRange("^2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.String(); got != "^2.0.0" {
		t.Errorf("String() = %q, want %q", got, "^2.0.0")
	}
}
