package diff

import (
	"encoding/json"
	"testing"
)

func TestClassification_String(t *testing.T) {
	tests := []struct {
		c    Classification
		want string
	}{
		{NonBreaking, "NON_BREAKING"},
		{PotentialBreaking, "POTENTIAL_BREAKING"},
		{Breaking, "BREAKING"},
		{Classification(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.c.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassification_MarshalJSON(t *testing.T) {
	tests := []struct {
		c    Classification
		want string
	}{
		{NonBreaking, `"NON_BREAKING"`},
		{Breaking, `"BREAKING"`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			data, err := json.Marshal(tt.c)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("MarshalJSON() = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestChangeType_String(t *testing.T) {
	tests := []struct {
		ct   ChangeType
		want string
	}{
		{Added, "added"},
		{Removed, "removed"},
		{Modified, "modified"},
		{ChangeType(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.ct.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangeType_MarshalJSON(t *testing.T) {
	tests := []struct {
		ct   ChangeType
		want string
	}{
		{Added, `"added"`},
		{Removed, `"removed"`},
		{Modified, `"modified"`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			data, err := json.Marshal(tt.ct)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("MarshalJSON() = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestClassify_UnknownPath(t *testing.T) {
	got := classify("unknown.path", Modified)
	if got != PotentialBreaking {
		t.Errorf("expected PotentialBreaking for unknown path, got %s", got)
	}
}
