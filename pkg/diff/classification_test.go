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

func TestClassify_IndexedPaths(t *testing.T) {
	tests := []struct {
		path string
		ct   ChangeType
		want Classification
	}{
		// Multi-config paths (indices stripped)
		{"configuration.configs[0]", Added, NonBreaking},
		{"configuration.configs[2]", Removed, Breaking},
		{"configuration.configs[0].schema", Modified, PotentialBreaking},
		{"configuration.configs[0].schema", Added, NonBreaking},
		{"configuration.configs[0].schema", Removed, Breaking},
		{"configuration.configs[0].ref", Modified, PotentialBreaking},
		{"configuration.configs[0].ref", Added, NonBreaking},
		{"configuration.configs[0].ref", Removed, Breaking},
		{"configuration.configs[0].name", Modified, PotentialBreaking},

		// Policy paths (indices stripped)
		{"policies[0]", Added, NonBreaking},
		{"policies[0]", Removed, PotentialBreaking},
		{"policies[0].schema", Modified, PotentialBreaking},
		{"policies[0].ref", Modified, PotentialBreaking},
		{"policies[0].ref", Added, NonBreaking},
		{"policies[0].ref", Removed, PotentialBreaking},
	}
	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.ct.String(), func(t *testing.T) {
			got := classify(tt.path, tt.ct)
			if got != tt.want {
				t.Errorf("classify(%q, %s) = %s, want %s", tt.path, tt.ct, got, tt.want)
			}
		})
	}
}
