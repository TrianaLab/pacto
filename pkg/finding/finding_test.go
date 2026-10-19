package finding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFinding_Fields(t *testing.T) {
	f := Finding{
		Code:         CodeStatelessPersistent,
		Severity:     SeverityError,
		Category:     CategoryStateMismatch,
		Subject:      SubjectRef{Kind: "service", Name: "payments"},
		ContractPath: "state.persistence.durability",
		Message:      "stateless services must use ephemeral durability",
	}
	if f.Severity != SeverityError {
		t.Fatalf("severity = %q, want error", f.Severity)
	}
	if f.Category != CategoryStateMismatch {
		t.Fatalf("category = %q", f.Category)
	}
}

func TestFinding_EvidenceRefs(t *testing.T) {
	f := Finding{
		Code:         CodeCapabilityAbsent,
		Severity:     SeverityError,
		Category:     CategoryRuntimeDrift,
		Subject:      SubjectRef{Kind: "capability", Name: "health"},
		ContractPath: "capabilities[type=health]",
		Message:      "capability absent at runtime",
		EvidenceRefs: []EvidenceRef{
			{Source: "kubernetes", ObservedAt: "2026-07-24T12:00:00Z"},
		},
	}
	if len(f.EvidenceRefs) != 1 {
		t.Fatalf("expected 1 evidence ref, got %d", len(f.EvidenceRefs))
	}
	if f.EvidenceRefs[0].Source != "kubernetes" {
		t.Errorf("source = %q, want kubernetes", f.EvidenceRefs[0].Source)
	}
}

// An evidence ref with no matched observation leaves Collector and DetectedAt off the wire.
func TestEvidenceRef_ProvenanceOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(EvidenceRef{Source: "k8s", ObservedAt: "2026-10-17T00:00:00Z"})
	if err != nil || string(data) != `{"source":"k8s","observedAt":"2026-10-17T00:00:00Z"}` {
		t.Errorf("set-level ref = %s (%v)", data, err)
	}
	data, _ = json.Marshal(EvidenceRef{Source: "k8s", ObservedAt: "t", Collector: "ebpf", DetectedAt: "d"})
	if string(data) != `{"source":"k8s","observedAt":"t","collector":"ebpf","detectedAt":"d"}` {
		t.Errorf("observation ref = %s", data)
	}
}

// A finding is camelCase on the wire since v4, but records stored by v3 carry the
// PascalCase keys. This payload is one such record verbatim: it must still decode,
// and through the strict decoder every storage edge uses, which rejects unknown
// keys and so would fail if a legacy key stopped matching its field.
func TestFinding_DecodesLegacyPascalCase(t *testing.T) {
	const legacy = `{"Code":"INTERFACE_ABSENT","Severity":"error","Category":"RuntimeDrift",` +
		`"Subject":{"Kind":"interface","Name":"api"},"ContractPath":"interfaces[0]",` +
		`"Message":"not served","EvidenceRefs":[{"Source":"kubernetes","ObservedAt":"2026-10-17T00:00:00Z",` +
		`"Collector":"k8s-observer","DetectedAt":"2026-10-17T00:00:01Z"}]}`
	want := Finding{
		Code: CodeInterfaceAbsent, Severity: SeverityError, Category: CategoryRuntimeDrift,
		Subject: SubjectRef{Kind: "interface", Name: "api"}, ContractPath: "interfaces[0]", Message: "not served",
		EvidenceRefs: []EvidenceRef{{Source: "kubernetes", ObservedAt: "2026-10-17T00:00:00Z",
			Collector: "k8s-observer", DetectedAt: "2026-10-17T00:00:01Z"}},
	}
	dec := json.NewDecoder(strings.NewReader(legacy))
	dec.DisallowUnknownFields()
	var got Finding
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("legacy decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy decode = %+v, want %+v", got, want)
	}
	out, _ := json.Marshal(got)
	const camel = `{"code":"INTERFACE_ABSENT","severity":"error","category":"RuntimeDrift",` +
		`"subject":{"kind":"interface","name":"api"},"contractPath":"interfaces[0]",` +
		`"message":"not served","evidenceRefs":[{"source":"kubernetes","observedAt":"2026-10-17T00:00:00Z",` +
		`"collector":"k8s-observer","detectedAt":"2026-10-17T00:00:01Z"}]}`
	if string(out) != camel {
		t.Fatalf("re-encoded = %s, want %s", out, camel)
	}
}
