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

// TestClassify_Completeness locks the classification for every (path, change
// type) the diff engine can actually emit, so no emitted change silently falls
// through to the PotentialBreaking default with an unintended severity.
func TestClassify_Completeness(t *testing.T) {
	tests := []struct {
		path string
		ct   ChangeType
		want Classification
	}{
		// Schema version
		{"pactoVersion", Modified, NonBreaking},

		// Owner subfields
		{"service.owner.team", Modified, NonBreaking},
		{"service.owner.dri", Added, NonBreaking},
		{"service.owner.contacts", Added, NonBreaking},
		{"service.owner.contacts[slack:#x]", Added, NonBreaking},

		// State (top-level in v2)
		{"state.type", Modified, Breaking},
		{"state.persistence.scope", Modified, Breaking},
		{"state.dataCriticality", Modified, PotentialBreaking},

		// Workload (top-level in v2)
		{"workload", Modified, Breaking},
		{"workload", Added, NonBreaking},

		// Capabilities
		{"capabilities", Added, NonBreaking},
		{"capabilities", Removed, PotentialBreaking},
	}
	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.ct.String(), func(t *testing.T) {
			if got := classify(tt.path, tt.ct); got != tt.want {
				t.Errorf("classify(%q, %s) = %s, want %s", tt.path, tt.ct, got, tt.want)
			}
		})
	}
}

// TestClassify_UnlistedTransitionsFallThrough pins the fall-through the rule
// table leans on. Every transition here is deliberately NOT in `rules`, because
// PotentialBreaking is already the answer the default gives and a row restating
// it is one whose deletion no test can detect. Asserting the absence is the
// point: re-adding any of these rows fails this test even when the value it
// carries is the same one the default would have produced.
func TestClassify_UnlistedTransitionsFallThrough(t *testing.T) {
	unlisted := []classificationKey{
		// Introducing a schema where there was none constrains a consumer who was
		// unconstrained — potentially breaking, never safe.
		{"configurations.schema", Added},
		{"policies.schema", Added},
		{"policies.schema", Removed},
		// A request body appearing or disappearing.
		{"openapi.request-body", Added},
		{"openapi.request-body", Removed},
		// Modified rows for anything the JSON walk descends into: the walk
		// classifies the inner differences, so the table is never asked.
		{"openapi.request-body", Modified},
		{"openapi.responses", Modified},
		{"asyncapi.channels", Modified},
		// And any path the table has never heard of.
		{"unknown.path", Modified},
	}
	for _, k := range unlisted {
		t.Run(k.Path+"_"+k.Type.String(), func(t *testing.T) {
			if c, ok := rules[k]; ok {
				t.Fatalf("{%q, %s} is in the rule table as %s; the default already answers PotentialBreaking", k.Path, k.Type, c)
			}
			if got := classify(k.Path, k.Type); got != PotentialBreaking {
				t.Errorf("classify(%q, %s) = %s, want POTENTIAL_BREAKING", k.Path, k.Type, got)
			}
		})
	}
}

func TestClassify_NameIndexedPaths(t *testing.T) {
	tests := []struct {
		path string
		ct   ChangeType
		want Classification
	}{
		// Configurations (name-indexed). The Added transitions live in
		// TestClassify_UnlistedTransitionsFallThrough.
		{"configurations", Added, NonBreaking},
		{"configurations", Removed, Breaking},
		{"configurations.schema", Modified, PotentialBreaking},
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

		// Indexed paths (array indices stripped before lookup). Both rows answer
		// something other than the default, so they prove the normalisation ran
		// rather than the fall-through.
		{"configurations[0].schema", Removed, Breaking},
		{"policies[1].ref", Added, NonBreaking},

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

// TestClassify_InterfaceContentPaths locks the AsyncAPI and gRPC rules, and
// proves that classify() normalises the bracketed paths the differs actually
// emit (e.g. "grpc.messages[Order].fields[id]") down to their rule keys.
func TestClassify_InterfaceContentPaths(t *testing.T) {
	tests := []struct {
		path string
		ct   ChangeType
		want Classification
	}{
		// OpenAPI, rule keys. Responses have no Modified rule for the same reason
		// AsyncAPI channels do not: present on both sides, they are deep-diffed.
		// TestDiffOpenAPI_RequestBodyModified and TestDiffOpenAPI_ResponseModified
		// prove that.
		{"openapi.responses", Added, NonBreaking},
		{"openapi.responses", Removed, Breaking},

		// AsyncAPI, rule keys. There is no Modified rule: a channel or operation
		// present on both sides is deep-diffed, so the engine never asks for one.
		// TestDiffAsyncAPI_ModifiedIsAlwaysDeepDiffed proves that.
		{"asyncapi.channels", Added, NonBreaking},
		{"asyncapi.channels", Removed, Breaking},
		{"asyncapi.operations", Added, NonBreaking},
		{"asyncapi.operations", Removed, Breaking},

		// AsyncAPI, emitted paths
		{"asyncapi.channels[payment.completed]", Removed, Breaking},
		{"asyncapi.operations[sendOrder]", Added, NonBreaking},

		// gRPC, rule keys
		{"grpc.services", Added, NonBreaking},
		{"grpc.services", Removed, Breaking},
		{"grpc.rpcs", Added, NonBreaking},
		{"grpc.rpcs", Removed, Breaking},
		{"grpc.rpcs", Modified, Breaking},
		{"grpc.messages", Added, NonBreaking},
		{"grpc.messages", Removed, Breaking},
		{"grpc.messages.fields", Added, NonBreaking},
		{"grpc.messages.fields", Removed, Breaking},
		{"grpc.messages.fields", Modified, Breaking},

		// gRPC, emitted paths
		{"grpc.services[FraudService]", Removed, Breaking},
		{"grpc.rpcs[FraudService.ReportFraud]", Modified, Breaking},
		{"grpc.messages[Order]", Removed, Breaking},
		{"grpc.messages[Order].fields[id]", Modified, Breaking},
		{"grpc.messages[Order].fields[id]", Added, NonBreaking},
		{"grpc.messages[Order].fields[id]", Removed, Breaking},
	}
	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.ct.String(), func(t *testing.T) {
			if got := classify(tt.path, tt.ct); got != tt.want {
				t.Errorf("classify(%q, %s) = %s, want %s", tt.path, tt.ct, got, tt.want)
			}
		})
	}
}
