package contract_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestRequireDeclared(t *testing.T) {
	for raw, missing := range map[string]string{
		"":                                     "pactoVersion, service.name, service.version",
		"[a]":                                  "pactoVersion, service.name, service.version",
		"service: {name: a, version: 1.0.0}\n": "pactoVersion",
		"pactoVersion: \"2.5\"\nservice: {version: 1.0.0}\n":          "service.name",
		"pactoVersion: \"2.5\"\nservice: {name: a}\n":                 "service.version",
		"pactoVersion: \"2.5\"\nservice: {name: a, version: 1.0.0}\n": "",
	} {
		c, err := contract.Parse(strings.NewReader(raw))
		if err != nil {
			c = &contract.Contract{} // "[a]": only the raw check matters
		}
		err = contract.RequireDeclared(c, []byte(raw))
		if missing == "" {
			if err != nil {
				t.Errorf("%q: %v", raw, err)
			}
			continue
		}
		if !errors.Is(err, contract.ErrUndeclaredContract) || !strings.Contains(err.Error(), missing+" not declared") {
			t.Errorf("%q: got %v, want %q missing", raw, err, missing)
		}
	}
}
