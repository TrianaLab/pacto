package strictjson

import (
	"errors"
	"strings"
	"testing"
)

type doc struct {
	Name string `json:"name"`
}

func TestDecode_ValidSingleValue(t *testing.T) {
	var d doc
	if err := Unmarshal([]byte(`{"name":"ok"}`), &d); err != nil {
		t.Fatalf("valid value must decode, got %v", err)
	}
	if d.Name != "ok" {
		t.Errorf("decoded %+v", d)
	}
	// Trailing whitespace/newline is not trailing data.
	if err := Unmarshal([]byte(`{"name":"ok"}`+"\n \t"), &d); err != nil {
		t.Errorf("trailing whitespace must be allowed, got %v", err)
	}
}

func TestDecode_RejectsTrailingData(t *testing.T) {
	cases := map[string]string{
		"second object":       `{"name":"a"}{"name":"b"}`,
		"trailing array":      `{"name":"a"}[]`,
		"trailing scalar":     `{"name":"a"}5`,
		"lone close brace":    `{"name":"a"}}`, // the case Decoder.More misses
		"lone close bracket":  `{"name":"a"}]`, // the other case Decoder.More misses
		"non-whitespace junk": `{"name":"a"}garbage`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var d doc
			err := Unmarshal([]byte(in), &d)
			if !errors.Is(err, ErrTrailingData) {
				t.Errorf("input %q must be ErrTrailingData, got %v", in, err)
			}
		})
	}
}

func TestDecode_RejectsUnknownFields(t *testing.T) {
	var d doc
	err := Unmarshal([]byte(`{"name":"a","surprise":1}`), &d)
	if err == nil || strings.Contains(err.Error(), "unexpected data") {
		t.Errorf("unknown field must be a decode error, got %v", err)
	}
}

func TestDecode_RejectsMalformed(t *testing.T) {
	var d doc
	if err := Unmarshal([]byte(`{bad`), &d); err == nil {
		t.Error("malformed JSON must error")
	}
}
