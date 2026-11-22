package ci

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCarriedRedacted(t *testing.T) {
	const secret = "https://u:tok@h.example/x?token=s"
	p := &Plan{Rows: []Row{{Impact: Impact{Gate: "clear"}, Carried: []Carried{{Field: "service.owner", Value: secret, Reason: secret, Signer: secret, Record: secret}}}}}
	p.Settle("repo")
	c := p.Rows[0].Carried[0]
	for _, s := range []string{c.Value, c.Reason, c.Signer, c.Record} {
		if strings.Contains(s, "tok") || !strings.Contains(s, "https://h.example/x (redacted)") {
			t.Errorf("not redacted: %q", s)
		}
	}
}

func TestCarriedJSON(t *testing.T) {
	out, _ := json.Marshal(Row{Carried: []Carried{{Field: "service.owner", Value: "billing", From: "registry", Kind: "declared"}}})
	if !strings.Contains(string(out), `"carried":[{"field":"service.owner","value":"billing","from":"registry","kind":"declared"}]`) {
		t.Fatalf("%s", out)
	}
	if out, _ := json.Marshal(Row{}); strings.Contains(string(out), "carried") {
		t.Fatalf("%s", out)
	}
}
