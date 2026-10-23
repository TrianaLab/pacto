package contract

import (
	"strings"
	"testing"
)

func TestParseMCPTools(t *testing.T) {
	tools, err := ParseMCPTools([]byte(`{"tools":[{"name":"refund","inputSchema":{"type":"object"}},{"name":"lookup"}]}`))
	if err != nil || len(tools) != 2 || tools[0].Name != "refund" || tools[0].InputSchema == nil || tools[1].OutputSchema != nil {
		t.Fatalf("got %+v, %v", tools, err)
	}
	for in, want := range map[string]string{
		`[`:                                     "not a tools/list result",
		`{}`:                                    "lists no tools",
		`{"tools":[{"description":"x"}]}`:       "tool 0 has no name",
		`{"tools":[{"name":"a"},{"name":"a"}]}`: `tool "a" is listed twice`,
	} {
		if _, err := ParseMCPTools([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", in, err, want)
		}
	}
}
