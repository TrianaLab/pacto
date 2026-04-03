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

func TestClassify_NameIndexedPaths(t *testing.T) {
	tests := []struct {
		path string
		ct   ChangeType
		want Classification
	}{
		// Configurations (name-indexed)
		{"configurations", Added, NonBreaking},
		{"configurations", Removed, Breaking},
		{"configurations.schema", Modified, PotentialBreaking},
		{"configurations.schema", Added, NonBreaking},
		{"configurations.schema", Removed, Breaking},
		{"configurations.ref", Modified, PotentialBreaking},
		{"configurations.ref", Added, NonBreaking},
		{"configurations.ref", Removed, Breaking},

		// Policies (name-indexed)
		{"policies", Added, NonBreaking},
		{"policies", Removed, PotentialBreaking},
		{"policies.schema", Modified, PotentialBreaking},
		{"policies.ref", Modified, PotentialBreaking},
		{"policies.ref", Added, NonBreaking},
		{"policies.ref", Removed, PotentialBreaking},

		// Indexed paths (array indices stripped before lookup)
		{"configurations[0].schema", Modified, PotentialBreaking},
		{"configurations[0].schema", Added, NonBreaking},
		{"configurations[0].schema", Removed, Breaking},
		{"policies[1].ref", Modified, PotentialBreaking},

		// Dependencies
		{"dependencies", Added, NonBreaking},
		{"dependencies", Removed, Breaking},
		{"dependencies.ref", Modified, PotentialBreaking},
		{"dependencies.compatibility", Modified, PotentialBreaking},
		{"dependencies.required", Modified, PotentialBreaking},
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
